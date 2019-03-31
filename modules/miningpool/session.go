package pool

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"time"
    "math"
    "unsafe"

    "sync"
    "sync/atomic"

	"SiaPrime/build"
	"SiaPrime/persist"
)

const (
	numSharesToAverage = 30
	// we can drop to 1% of the highest difficulty before we decide we're disconnected
	maxDifficultyDropRatio = 0.01
	// how long we allow the session to linger if we haven't heard from the worker
	heartbeatTimeout = 3 * time.Minute
	// maxJob define how many time could exist in one session
	maxJob = 5
)

var (
	initialDifficulty = build.Select(build.Var{
		Standard: 6400.0,
		Dev:      6400.0,
		Testing:  0.00001,
	}).(float64) // change from 6.0 to 1.0
)

//
// A Session captures the interaction with a miner client from when they connect until the connection is
// closed.  A session is tied to a single client and has many jobs associated with it
//
type Session struct {
	ExtraNonce1           uint32
	//authorized          bool
    authorized            uint32
	//disableVarDiff      bool
    disableVarDiff        uint64
	SessionID             uint64
	//currentDifficulty   float64
	currentDifficulty     uint64
	//highestDifficulty   float64
	highestDifficulty     uint64
	vardiff               Vardiff
	lastShareSpot         uint64
	Client                *Client
	CurrentWorker         *Worker
	CurrentShift          *Shift
	log                   *persist.Logger
	CurrentJobs           []*Job
	lastJobTimestamp      time.Time
	shareTimes            [numSharesToAverage]time.Time
	lastVardiffRetarget   time.Time
	lastVardiffTimestamp  time.Time
	sessionStartTimestamp time.Time
	lastHeartbeat         time.Time
	mu                    sync.RWMutex

	clientVersion  string
	remoteAddr     string
}

func newSession(p *Pool, ip string) (*Session, error) {
	id := p.newStratumID()
	s := &Session{
		//SessionID:            id(),
		ExtraNonce1:          uint32(id() & 0xffffffff),
		//currentDifficulty:    initialDifficulty,
		//highestDifficulty:    initialDifficulty,
		lastVardiffRetarget:  time.Now(),
		lastVardiffTimestamp: time.Now(),
		//disableVarDiff:       false,
		remoteAddr:           ip,
	}

    s.SetID(id())
    s.SetHighestDifficulty(initialDifficulty)
    s.SetCurrentDifficulty(initialDifficulty)
    s.SetDisableVarDiff(false)
	s.vardiff = *s.newVardiff()

	s.sessionStartTimestamp = time.Now()
	s.SetHeartbeat()

	return s, nil
}

func (s *Session) addClient(c *Client) {
    atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&s.Client)), unsafe.Pointer(c))
}
func (s *Session) GetClient() *Client {
    return (*Client)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&s.Client))))
}

func (s *Session) addWorker(w *Worker) {
    atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&s.CurrentWorker)), unsafe.Pointer(w))
	w.SetSession(s)
}
func (s *Session) GetCurrentWorker() *Worker {
    return (*Worker)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&s.CurrentWorker))))
}

func (s *Session) addJob(j *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.CurrentJobs) >= maxJob {
		s.CurrentJobs = s.CurrentJobs[len(s.CurrentJobs)-maxJob+1:]
	}

	s.CurrentJobs = append(s.CurrentJobs, j)
	s.lastJobTimestamp = time.Now()
}

func (s *Session) getJob(jobID uint64, nonce string) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	//s.log.Printf("submit id:%d, before pop len:%d\n", jobID, len(s.CurrentJobs))
	for _, j := range s.CurrentJobs {
		// s.log.Printf("i: %d, array id: %d\n", i, j.JobID)
		if jobID == j.JobID {
			//s.log.Printf("get job len:%d\n", len(s.CurrentJobs))
			if _, ok := j.SubmitedNonce[nonce]; ok {
				return nil, errors.New("already submited nonce for this job")
			}
			return j, nil
		}
	}

	return nil, nil // for stale/unkonwn job response
}

func (s *Session) clearJobs() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentJobs = nil
}

func (s *Session) addShift(shift *Shift) {
    atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&s.CurrentShift)), unsafe.Pointer(shift))
}

// Shift returns the current Shift associated with a session
func (s *Session) Shift() *Shift {
    return (*Shift)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&s.CurrentShift))))
}

func (s *Session) GetID() uint64 {
    return atomic.LoadUint64(&s.SessionID)
}

func (s *Session) SetID(id uint64) {
    atomic.StoreUint64(&s.SessionID, id)
}

func (s *Session) printID() string {
    return sPrintID(atomic.LoadUint64(&s.SessionID))
}

func (s *Session) printNonce() string {
    extranonce1 := atomic.LoadUint32(&s.ExtraNonce1)
	ex1 := make([]byte, 4)
	binary.LittleEndian.PutUint32(ex1, extranonce1)
	return hex.EncodeToString(ex1)
}

// SetLastShareTimestamp add a new time stamp
func (s *Session) SetLastShareTimestamp(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.shareTimes[s.lastShareSpot] = t
	s.lastShareSpot++
	if s.lastShareSpot == s.vardiff.bufSize {
		s.lastShareSpot = 0
	}
}

// ShareDurationAverage caculate the average duration of the
func (s *Session) ShareDurationAverage() (float64, float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var minTime, maxTime time.Time
	var timestampCount int

	for i := uint64(0); i < s.vardiff.bufSize; i++ {
		// s.log.Printf("ShareDurationAverage: %d %s %t\n", i, s.shareTimes[i], s.shareTimes[i].IsZero())
		if s.shareTimes[i].IsZero() {
			continue
		}
		timestampCount++
		if minTime.IsZero() {
			minTime = s.shareTimes[i]
		}
		if maxTime.IsZero() {
			maxTime = s.shareTimes[i]
		}
		if s.shareTimes[i].Before(minTime) {
			minTime = s.shareTimes[i]
		}
		if s.shareTimes[i].After(maxTime) {
			maxTime = s.shareTimes[i]
		}
	}

	var unsubmitStart time.Time
	if maxTime.IsZero() {
		unsubmitStart = s.sessionStartTimestamp
	} else {
		unsubmitStart = maxTime
	}
	unsubmitDuration := time.Now().Sub(unsubmitStart).Seconds()
	if timestampCount < 2 { // less than 2 stamp
		return unsubmitDuration, 0
	}

	historyDuration := maxTime.Sub(minTime).Seconds() / float64(timestampCount-1)

	return unsubmitDuration, historyDuration
}

// IsStable checks if the session has been running long enough to fill up the
// vardiff buffer
func (s *Session) IsStable() bool {
	if s.shareTimes[s.vardiff.bufSize-1].IsZero() {
		return false
	}
	return true
}

// CurrentDifficulty returns the session's current difficulty
func (s *Session) CurrentDifficulty() float64 {
    return math.Float64frombits(atomic.LoadUint64(&s.currentDifficulty))
}

// HighestDifficulty returns the highest difficulty the session has seen
func (s *Session) HighestDifficulty() float64 {
    return math.Float64frombits(atomic.LoadUint64(&s.highestDifficulty))
}

// SetHighestDifficulty records the highest difficulty the session has seen
func (s *Session) SetHighestDifficulty(d float64) {
    atomic.StoreUint64(&s.highestDifficulty, math.Float64bits(d))
}

// SetCurrentDifficulty sets the current difficulty for the session
func (s *Session) SetCurrentDifficulty(d float64) {
    if d > s.HighestDifficulty() {
        d = s.HighestDifficulty()
    }
    atomic.StoreUint64(&s.currentDifficulty, math.Float64bits(d))
}

// SetClientVersion sets the current client version for the session
func (s *Session) SetClientVersion(v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clientVersion = v
}

// SetDisableVarDiff sets the disable var diff flag for the session
func (s *Session) SetDisableVarDiff(flag bool) {
    if flag {
        atomic.StoreUint64(&s.disableVarDiff, 1)
    } else {
        atomic.StoreUint64(&s.disableVarDiff, 0)
    }
}

func (s *Session) GetDisableVarDiff() bool {
    flag := atomic.LoadUint64(&s.disableVarDiff)
    if flag == 1 {
        return true
    }
    return false
}

// DetectDisconnected checks to see if we haven't heard from a client for too
// long of a time. It does this via 2 mechanisms:
// 1) how long ago was the last share submitted? (the hearbeat)
// 2) how low has the difficulty dropped from the highest difficulty the client
//    ever faced?
func (s *Session) DetectDisconnected() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.log != nil {
		//s.log.Printf("time now: %s, last beat: %s, disconnect: %t\n", time.Now(),s.lastHeartbeat, time.Now().After(s.lastHeartbeat.Add(heartbeatTimeout)))
	}
	// disconnect if we haven't heard from the worker for a long time
	if time.Now().After(s.lastHeartbeat.Add(heartbeatTimeout)) {
		if s.log != nil {
			//s.log.Printf("Disconnect because heartbeat time")
		}
		return true
	}
	// disconnect if the worker's difficulty has dropped too far from it's historical diff
	if (s.CurrentDifficulty() / s.HighestDifficulty()) < maxDifficultyDropRatio {
		return true
	}
	return false
}

// SetAuthorized specifies whether or not the session has been authorized
func (s *Session) SetAuthorized(b bool) {
    if b {
        atomic.StoreUint32(&s.authorized, 1)
    } else {
        atomic.StoreUint32(&s.authorized, 0)
    }
}

// Authorized returns whether or not the session has been authorized
func (s *Session) Authorized() bool {
    flag := atomic.LoadUint32(&s.authorized)
    if flag == 1 {
        return true
    }
    return false
}

// SetHeartbeat indicates that we just received a share submission
func (s *Session) SetHeartbeat() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastHeartbeat = time.Now()
}
