package pool

import (
    "bufio"
    "bytes"
    "encoding/binary"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    // "math/big"
    "net"
    "strconv"
    "strings"
    "time"
    "unsafe"

    "sync"
    "sync/atomic"

    "SiaPrime/encoding"
    "SiaPrime/modules"
    "SiaPrime/persist"
    "SiaPrime/types"
)

const (
    extraNonce2Size = 4
)

// Handler represents the status (open/closed) of each connection
type Handler struct {
    mu     sync.RWMutex
    conn   net.Conn
    ready  chan bool
    closed chan bool
    notify chan bool
    p      *Pool
    log    *persist.Logger
    s      *Session
}

const (
    blockTimeout = 5 * time.Second
    // This should represent the max number of pending notifications (new blocks found) within blockTimeout seconds
    // better to have too many, than not enough (causes a deadlock otherwise)
    numPendingNotifies = 5
)

func (h *Handler) SetSession(s *Session) {
    atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&h.s)), unsafe.Pointer(s))
}

func (h *Handler) GetSession() *Session {
    return (*Session)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&h.s))))
}

func (h *Handler) setupNotifier() {
    err := h.p.tg.Add()
	if err != nil {
		// If this goroutine is not run before shutdown starts, this
		// codeblock is reachable.
		return
	}

    defer func() {
        h.p.tg.Done()
        h.closed <- true
        h.conn.Close()
        if h.GetSession() != nil && h.GetSession().GetCurrentWorker() != nil {
            // delete a worker record when a session disconnections
            h.GetSession().GetCurrentWorker().deleteWorkerRecord()
        }
    }()

    for {
        select {
        case <-h.p.tg.StopChan():
            return
        case <-h.closed:
            return
        case <-h.notify:
            var m types.StratumRequest
            m.Method = "mining.notify"
            err := h.handleRequest(&m)

            //connection already broken, giving up
            if err != nil {
                if h.GetSession() != nil && h.GetSession().GetClient() != nil {
                    h.log.Printf("%s: error notifying worker.\n", h.GetSession().GetClient().cr.name)
                }
                return
            }
        }
    }
}

func (h *Handler) parseRequest() (*types.StratumRequest, error) {
    var m types.StratumRequest
    h.conn.SetReadDeadline(time.Now().Add(blockTimeout))
    //dec := json.NewDecoder(h.conn)
    buf := bufio.NewReader(h.conn)
    select {
    case <-h.p.tg.StopChan():
        return nil, errors.New("Stopping handler")
    default:
        // try reading from the connection
        //err = dec.Decode(&m)
        str, err := buf.ReadString('\n')
        // if we timeout
        if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
            //h.conn.SetReadDeadline(time.Time{})
            // check last job time and if over 25 seconds, send a new job.
            // if time.Now().Sub(h.s.lastJobTimestamp) > (time.Second * 25) {
            // 	m.Method = "mining.notify"
            // 	break
            // }
            if h.GetSession().DetectDisconnected() {
                return nil, errors.New("Non-responsive disconnect detected")
            }

            // h.log.Printf("Non-responsive disconnect ratio: %f\n", ratio)

            if h.GetSession().checkDiffOnNewShare() {
                err = h.sendSetDifficulty(h.GetSession().CurrentDifficulty())
                if err != nil {
                    return nil, err
                }
                err = h.sendStratumNotify(true)
                if err != nil {
                    return nil, err
                }
            }

            return nil, nil
            // if we don't timeout but have some other error
        } else if err != nil {
            if err == io.EOF {
                //h.log.Println("End connection")
            } else {
                //h.log.Println(err)
                //reset by peer
            }
            return nil, err
        } else {
            // NOTE: we were getting weird cases where the buffer would read a full
            // string, then read part of the string again (like the last few chars).
            // Somehow it seemed like the socket data wasn't getting purged correctly
            // on reading.
            // Not seeing it lately, but that's why we have this debugging code,
            // and that's why we don't just read straight into the JSON decoder.
            // Reading the data into a string buffer lets us debug more easily.

            // If we encounter this issue again, we may want to drop down into
            // lower-level socket manipulation to try to troubleshoot it.
            // h.log.Println(str)
            dec := json.NewDecoder(strings.NewReader(str))
            err = dec.Decode(&m)
            if err != nil {
                //h.log.Println(err)
                //h.log.Println(str)
                //return nil, err
                // don't disconnect here, just treat it like a harmless timeout
                return nil, nil
            }
        }
    }
    return &m, nil
}

func (h *Handler) handleRequest(m *types.StratumRequest) error {
    h.GetSession().SetHeartbeat()
    var err error
    switch m.Method {
    case "mining.subscribe":
        return h.handleStratumSubscribe(m)
    case "mining.authorize":
        err = h.handleStratumAuthorize(m)
        if err != nil {
            h.log.Printf("Failed to authorize client\n")
            h.log.Println(err)
            return err
        }
        return h.sendStratumNotify(true)
    case "mining.extranonce.subscribe":
        return h.handleStratumNonceSubscribe(m)
    case "mining.submit":
        return h.handleStratumSubmit(m)
    case "mining.notify":
        return h.sendStratumNotify(true)
    default:
        h.log.Debugln("Unknown stratum method: ", m.Method)
    }
    return nil
}

// Listen listens on a connection for incoming data and acts on it
func (h *Handler) Listen() {
    defer func() {
        h.closed <- true // send to dispatcher, that connection is closed
        h.conn.Close()
        if h.GetSession() != nil && h.GetSession().GetCurrentWorker() != nil {
            // delete a worker record when a session disconnections
            h.GetSession().GetCurrentWorker().deleteWorkerRecord()
            // when we shut down the pool we get an error here because the log
            // is already closed... TODO
        }
    }()
    err := h.p.tg.Add()
    if err != nil {
        // If this goroutine is not run before shutdown starts, this
        // codeblock is reachable.
        return
    }
    defer h.p.tg.Done()

    s, _ := newSession(h.p, h.conn.RemoteAddr().String())
    h.SetSession(s)
    h.ready <- true
    for {
        m, err := h.parseRequest()
        h.conn.SetReadDeadline(time.Time{})
        // if we timed out
        if m == nil && err == nil {
            continue
            // else if we got a request
        } else if m != nil {
            err = h.handleRequest(m)
            if err != nil {
                h.log.Println(err)
                return
            }
            // else if we got an error
        } else if err != nil {
            //h.log.Println(err)
            return
        }
    }
}

func (h *Handler) sendResponse(r types.StratumResponse) error {
    b, err := json.Marshal(r)
    //fmt.Printf("SERVER: %s\n", b)
    if err != nil {
        h.log.Debugln("json marshal failed for id: ", r.ID, err)
        return err
    }
    b = append(b, '\n')
    _, err = h.conn.Write(b)
    if err != nil {
        h.log.Debugln("connection write failed for id: ", r.ID, err)
        return err
    }
    return nil
}

func (h *Handler) sendRequest(r types.StratumRequest) error {
    b, err := json.Marshal(r)
    if err != nil {
        h.log.Debugln("json marshal failed for id: ", r.ID, err)
        return err
    }
    b = append(b, '\n')
    _, err = h.conn.Write(b)
    if err != nil {
        h.log.Debugln("connection write failed for id: ", r.ID, err)
        return err
    }
    return nil
}

// handleStratumSubscribe message is the first message received and allows the pool to tell the miner
// the difficulty as well as notify, extranonce1 and extranonce2
//
// TODO: Pull the appropriate data from either in memory or persistent store as required
func (h *Handler) handleStratumSubscribe(m *types.StratumRequest) error {
    if len(m.Params) > 0 {
        //h.log.Printf("Client subscribe name:%s", m.Params[0].(string))
        h.GetSession().SetClientVersion(m.Params[0].(string))
    }

    if len(m.Params) > 0 && m.Params[0].(string) == "sgminer/4.4.2" {
        h.GetSession().SetHighestDifficulty(11500)
        h.GetSession().SetCurrentDifficulty(11500)
        h.GetSession().SetDisableVarDiff(true)
    }
    if len(m.Params) > 0 && m.Params[0].(string) == "cgminer/4.9.0" {
        h.GetSession().SetHighestDifficulty(1024)
        h.GetSession().SetCurrentDifficulty(1024)
        h.GetSession().SetDisableVarDiff(true)
    }
    if len(m.Params) > 0 && m.Params[0].(string) == "cgminer/4.10.0" {
        h.GetSession().SetHighestDifficulty(700)
        h.GetSession().SetCurrentDifficulty(700)
        h.GetSession().SetDisableVarDiff(true)
    }
    if len(m.Params) > 0 && m.Params[0].(string) == "gominer" {
        h.GetSession().SetHighestDifficulty(0.03)
        h.GetSession().SetCurrentDifficulty(0.03)
        h.GetSession().SetDisableVarDiff(true)
    }
    if len(m.Params) > 0 && m.Params[0].(string) == "NiceHash/1.0.0" {
        r := types.StratumResponse{ID: m.ID}
        r.Result = false
        r.Error = interfaceify([]string{"NiceHash client is not supported"})
        return h.sendResponse(r)
    }

    r := types.StratumResponse{ID: m.ID}
    r.Method = m.Method
    /*if !h.s.Authorized() {
        r.Result = false
        r.Error = interfaceify([]string{"Session not authorized - authorize before subscribe"})
        return h.sendResponse(r)
    }
    */

    //	diff := "b4b6693b72a50c7116db18d6497cac52"
    t, _ := h.p.persist.Target.Difficulty().Uint64()
    h.log.Debugf("Block Difficulty: %x\n", t)
    tb := make([]byte, 8)
    binary.LittleEndian.PutUint64(tb, t)
    diff := hex.EncodeToString(tb)
    //notify := "ae6812eb4cd7735a302a8a9dd95cf71f"
    notify := h.GetSession().printID()
    extranonce1 := h.GetSession().printNonce()
    extranonce2 := extraNonce2Size
    raw := fmt.Sprintf(`[ [ ["mining.set_difficulty", "%s"], ["mining.notify", "%s"]], "%s", %d]`, diff, notify, extranonce1, extranonce2)
    r.Result = json.RawMessage(raw)
    r.Error = nil
    return h.sendResponse(r)
}

// this is thread-safe, when we're looking for and possibly creating a client,
// we need to make sure the action is atomic
func (h *Handler) setupClient(client, worker string) (*Client, error) {
    var err error
    h.p.mu.Lock()
    lock, exists := h.p.clientSetupMutex[client]
    if !exists {
        lock = &sync.Mutex{}
        h.p.clientSetupMutex[client] = lock
    }
    h.p.mu.Unlock()
    lock.Lock()
    defer lock.Unlock()
    c, err := h.p.FindClientDB(client)
    if err == ErrQueryTimeout {
        return c, err
    } else if err != ErrNoUsernameInDatabase {
        return c, err
    }
    if c == nil {
        //fmt.Printf("Unable to find client in db: %s\n", client)
        c, err = newClient(h.p, client)
        if err != nil {
            //fmt.Println("Failed to create a new Client")
            h.p.log.Printf("Failed to create a new Client: %s\n", err)
            return nil, err
        }
        err = h.p.AddClientDB(c)
        if err != nil {
            h.p.log.Printf("Failed to add client to DB: %s\n", err)
            return nil, err
        }
    } else {
        //fmt.Printf("Found client: %s\n", client)
    }
    if h.p.Client(client) == nil {
        h.p.log.Printf("Adding client in memory: %s\n", client)
        h.p.AddClient(c)
    }
    return c, nil
}

func (h *Handler) setupWorker(c *Client, workerName string) (*Worker, error) {
    w, err := newWorker(c, workerName, h.GetSession())
    if err != nil {
        c.log.Printf("Failed to add worker: %s\n", err)
        return nil, err
    }

    err = c.addWorkerDB(w)
    if err != nil {
        c.log.Printf("Failed to add worker: %s\n", err)
        return nil, err
    }
    h.GetSession().log = w.log
    w.log.Printf("Adding new worker: %s, %d\n", workerName, w.GetID())
    h.log.Debugln("client = " + c.Name() + ", worker = " + workerName)
    return w, nil
}

// handleStratumAuthorize allows the pool to tie the miner connection to a particular user
func (h *Handler) handleStratumAuthorize(m *types.StratumRequest) error {
    var err error

    r := types.StratumResponse{ID: m.ID}

    r.Method = "mining.authorize"
    r.Result = true
    r.Error = nil
    clientName := m.Params[0].(string)
    passwordField := m.Params[1].(string)
    workerName := ""
    if strings.Contains(clientName, ".") {
        s := strings.SplitN(clientName, ".", -1)
        clientName = s[0]
        workerName = s[1]
    }
    //custom difficulty, format: x,d=1024
    if strings.Contains(passwordField, ",") {
        s1 := strings.SplitN(passwordField, ",", -1)
        difficultyStr := s1[1]
        if strings.Contains(difficultyStr, "=") {
            difficultyVals := strings.SplitN(difficultyStr, "=", 2)
            difficulty, err := strconv.ParseFloat(difficultyVals[1], 64)
            if err != nil {
                r.Result = false
                r.Error = interfaceify([]string{"Invalid difficulty value"})
                err = errors.New("Invalid custom difficulty value")
                h.sendResponse(r)
                return err
            }
            h.GetSession().SetHighestDifficulty(difficulty)
            h.GetSession().SetCurrentDifficulty(difficulty)
            h.GetSession().SetDisableVarDiff(true)
        }
    }

    // load wallet and check validity
    var walletTester types.UnlockHash
    err = walletTester.LoadString(clientName)
    if err != nil {
        r.Result = false
        r.Error = interfaceify([]string{"Client Name must be valid wallet address"})
        h.log.Printf("Client Name must be valid wallet address. Client name is: %s\n", clientName)
        err = errors.New("Client name must be a valid wallet address")
        h.sendResponse(r)
        return err
    }

    c, err := h.setupClient(clientName, workerName)
    if err != nil {
        r.Result = false
        r.Error = interfaceify([]string{err.Error()})
        h.sendResponse(r)
        return err
    }
    w, err := h.setupWorker(c, workerName)
    if err != nil {
        r.Result = false
        r.Error = interfaceify([]string{err.Error()})
        h.sendResponse(r)
        return err
    }

    // if everything is fine, setup the client and send a response and difficulty
    h.GetSession().addClient(c)
    h.GetSession().addWorker(w)
    h.GetSession().addShift(h.p.newShift(h.GetSession().GetCurrentWorker()))
    h.GetSession().SetAuthorized(true)
    err = h.sendResponse(r)
    if err != nil {
        return err
    }
    err = h.sendSetDifficulty(h.GetSession().CurrentDifficulty())
    if err != nil {
        return err
    }
    return nil
}

// handleStratumNonceSubscribe tells the pool that this client can handle the extranonce info
// TODO: Not sure we have to anything if all our clients support this.
func (h *Handler) handleStratumNonceSubscribe(m *types.StratumRequest) error {
    h.p.log.Debugln("ID = "+strconv.FormatUint(m.ID, 10)+", Method = "+m.Method+", params = ", m.Params)

    // not sure why 3 is right, but ccminer expects it to be 3
    r := types.StratumResponse{ID: 3}
    r.Result = true
    r.Error = nil

    return h.sendResponse(r)
}

// request is sent as [name, jobid, extranonce2, nTime, nonce]
func (h *Handler) handleStratumSubmit(m *types.StratumRequest) error {
    r := types.StratumResponse{ID: m.ID}
    r.Method = "mining.submit"
    r.Result = true
    r.Error = nil
    /*
    err := json.Unmarshal(m.Params, &p)
    if err != nil {
        h.log.Printf("Unable to parse mining.submit params: %v\n", err)
        r.Result = false //json.RawMessage(`false`)
        r.Error = interfaceify([]string{"20","Parse Error"}) //json.RawMessage(`["20","Parse Error"]`)
    }
    */
    // name := m.Params[0].(string)
    var jobID uint64
    fmt.Sscanf(m.Params[1].(string), "%x", &jobID)
    extraNonce2 := m.Params[2].(string)
    nTime := m.Params[3].(string)
    nonce := m.Params[4].(string)

    needNewJob := false
    defer func() {
        if needNewJob == true {
            h.sendStratumNotify(true)
        }
    }()

    if h.GetSession().GetCurrentWorker() == nil {
        // worker failed to authorize
        r.Result = false
        r.Error = interfaceify([]string{"24", "Unauthorized worker"})
        if h.GetSession().GetClient() != nil {
            h.log.Printf("%s: Unauthorized worker\n", h.GetSession().GetClient().Name())
        }
        return h.sendResponse(r)
        return errors.New("Worker failed to authorize - dropping")
    }

    h.GetSession().SetLastShareTimestamp(time.Now())
    sessionPoolDifficulty := h.GetSession().CurrentDifficulty()
    if h.GetSession().checkDiffOnNewShare() {
        h.sendSetDifficulty(h.GetSession().CurrentDifficulty())
        needNewJob = true
    }

    j, err := h.GetSession().getJob(jobID, nonce)
    if err != nil {
        r.Result = false
        r.Error = interfaceify([]string{"21", err.Error()}) //json.RawMessage(`["21","Stale - old/unknown job"]`)
        h.GetSession().GetCurrentWorker().IncrementInvalidShares()
        return h.sendResponse(r)
    }
    var b types.Block
    if j != nil {
        encoding.Unmarshal(j.MarshalledBlock, &b)
    }

    if len(b.MinerPayouts) == 0 {
        r.Result = false
        r.Error = interfaceify([]string{"22", "Stale - old/unknown job"}) //json.RawMessage(`["22","Stale - old/unknown job"]`)
        if h.GetSession().GetClient() != nil {
            h.p.log.Printf("%s.%s: Stale Share rejected - old/unknown job\n", h.GetSession().GetClient().Name(), h.GetSession().GetCurrentWorker().Name())
        }
        h.GetSession().GetCurrentWorker().IncrementInvalidShares()
        return h.sendResponse(r)
    }

    bhNonce, err := hex.DecodeString(nonce)
    copy(b.Nonce[:], bhNonce[0:8])
    tb, _ := hex.DecodeString(nTime)
    b.Timestamp = types.Timestamp(encoding.DecUint64(tb))

    cointxn := h.p.coinB1()
    ex1, _ := hex.DecodeString(h.GetSession().printNonce())
    ex2, _ := hex.DecodeString(extraNonce2)
    cointxn.ArbitraryData[0] = append(cointxn.ArbitraryData[0], ex1...)
    cointxn.ArbitraryData[0] = append(cointxn.ArbitraryData[0], ex2...)

    b.Transactions = append(b.Transactions, []types.Transaction{cointxn}...)
    blockHash := b.ID()
    // bh := new(big.Int).SetBytes(blockHash[:])

    sessionPoolTarget, _ := difficultyToTarget(sessionPoolDifficulty)

    // 	sessionPoolDifficulty, printWithSuffix(sessionPoolTarget.Difficulty()))

    // need to checkout the block hashrate reach pool target or not
    if bytes.Compare(sessionPoolTarget[:], blockHash[:]) < 0 {
        r.Result = false
        r.Error = interfaceify([]string{"23", "Low difficulty share"}) //23 - Low difficulty share
        if h.GetSession().GetClient() != nil {
            h.p.log.Printf("%s.%s: Low difficulty share\n", h.GetSession().GetClient().Name(), h.GetSession().GetCurrentWorker().Name())
        }
        h.GetSession().GetCurrentWorker().IncrementInvalidShares()
        return h.sendResponse(r)
    }

    t := h.p.persist.GetTarget()
    // printWithSuffix(types.IntToTarget(bh).Difficulty()), printWithSuffix(t.Difficulty()))
    if bytes.Compare(t[:], blockHash[:]) < 0 {
        h.GetSession().GetCurrentWorker().IncrementShares(h.GetSession().CurrentDifficulty(), currencyToAmount(b.MinerPayouts[0].Value))
        h.GetSession().GetCurrentWorker().SetLastShareTime(time.Now())
        return h.sendResponse(r)
    }
    err = h.p.managedSubmitBlock(b)
    if err != nil && err != modules.ErrBlockUnsolved {
        h.log.Printf("Failed to SubmitBlock(): %v\n", err)
        h.log.Printf(sPrintBlock(b))
        //panic(fmt.Sprintf("Failed to SubmitBlock(): %v\n", err))
        r.Result = false //json.RawMessage(`false`)
        r.Error = interfaceify([]string{"20", "Stale share"})
        h.GetSession().GetCurrentWorker().IncrementInvalidShares()
        return h.sendResponse(r)
    }

    h.GetSession().GetCurrentWorker().IncrementShares(h.GetSession().CurrentDifficulty(), currencyToAmount(b.MinerPayouts[0].Value))
    h.GetSession().GetCurrentWorker().SetLastShareTime(time.Now())

    if err == nil {
        h.p.log.Println("Yay!!! Solved a block!!")
        h.GetSession().clearJobs()
        err = h.GetSession().GetCurrentWorker().addFoundBlock(&b)
        if err != nil {
            h.p.log.Printf("Failed to update block in database: %s\n", err)
        }
        h.p.shiftChan <- true
    }

    //else block unsolved
    return h.sendResponse(r)
}

func (h *Handler) sendSetDifficulty(d float64) error {
    var r types.StratumRequest

    r.Method = "mining.set_difficulty"
    // assuming this ID is the response to the original subscribe which appears to be a 1
    r.ID = 0
    params := make([]interface{}, 1)
    r.Params = params
    r.Params[0] = d
    return h.sendRequest(r)
}

func (h *Handler) sendStratumNotify(cleanJobs bool) error {
    var r types.StratumRequest
    r.Method = "mining.notify"
    r.ID = 0 // gominer requires notify to use an id of 0
    job, _ := newJob(h.p)
    h.GetSession().addJob(job)
    jobid := job.printID()
    var b types.Block
    h.p.persist.mu.Lock()
    // make a copy of the block and hold it until the solution is submitted
    job.MarshalledBlock = encoding.Marshal(&h.p.sourceBlock)
    h.p.persist.mu.Unlock()
    encoding.Unmarshal(job.MarshalledBlock, &b)
    job.MerkleRoot = b.MerkleRoot()
    mbj := b.MerkleBranches()
    h.log.Debugf("merkleBranch: %s\n", mbj)

    version := ""
    nbits := fmt.Sprintf("%08x", BigToCompact(h.p.persist.Target.Int()))

    var buf bytes.Buffer
    encoding.WriteUint64(&buf, uint64(b.Timestamp))
    ntime := hex.EncodeToString(buf.Bytes())

    params := make([]interface{}, 9)
    r.Params = params
    r.Params[0] = jobid
    r.Params[1] = b.ParentID.String()
    r.Params[2] = h.p.coinB1Txn()
    r.Params[3] = h.p.coinB2()
    r.Params[4] = mbj
    r.Params[5] = version
    r.Params[6] = nbits
    r.Params[7] = ntime
    r.Params[8] = cleanJobs
    //h.log.Debugf("send.notify: %s\n", raw)
    h.log.Debugf("send.notify: %s\n", r.Params)
    return h.sendRequest(r)
}
