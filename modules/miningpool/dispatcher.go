package pool

import (
	// "fmt"

	"net"
	"time"

    "sync"
    "sync/atomic"
	//"github.com/sasha-s/go-deadlock"

	"SiaPrime/persist"
)

// Dispatcher contains a map of ip addresses to handlers
// Dispatcher contains a map of ip addresses to handlers
type Dispatcher struct {
	handlers          map[string]*Handler
	ln                net.Listener
	mu                sync.RWMutex
	p                 *Pool
	log               *persist.Logger
	connectionsOpened uint64
}

// NumConnections returns the number of open tcp connections
func (d *Dispatcher) NumConnections() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.handlers)
}

// NumConnectionsOpened returns the number of tcp connections that the pool
// has ever opened
func (d *Dispatcher) NumConnectionsOpened() uint64 {
	return atomic.LoadUint64(&d.connectionsOpened)
}

// IncrementConnectionsOpened increments the number of tcp connections that the
// pool has ever opened
func (d *Dispatcher) IncrementConnectionsOpened() {
    atomic.AddUint64(&d.connectionsOpened, 1)
}

//AddHandler connects the incoming connection to the handler which will handle it
func (d *Dispatcher) AddHandler(conn net.Conn) {
    tcpconn := conn.(*net.TCPConn)
    tcpconn.SetKeepAlive(true)
    //tcpconn.SetKeepAlivePeriod(30 * time.Second)
    tcpconn.SetKeepAlivePeriod(15 * time.Second)
    tcpconn.SetNoDelay(true)
    // maybe this will help with our disconnection problems
    tcpconn.SetLinger(2)

	addr := conn.RemoteAddr().String()
	handler := &Handler{
		conn:   conn,
        ready:  make(chan bool, 1),
		closed: make(chan bool, 2),
		notify: make(chan bool, numPendingNotifies),
		p:      d.p,
		log:    d.log,
	}
	d.mu.Lock()
	d.handlers[addr] = handler
	d.mu.Unlock()

    go d.AddNotifier(handler)
	// fmt.Printf("AddHandler listen() called: %s\n", addr)
	handler.Listen()

	<-handler.closed // when connection closed, remove handler from handlers
    d.log.Println("handler releasing")
	d.mu.Lock()
	delete(d.handlers, addr)
	//fmt.Printf("Exiting AddHandler, %d connections remaining\n", len(d.handlers))
	d.mu.Unlock()
    d.log.Println("handler release done")
}

func (d *Dispatcher) AddNotifier(h *Handler) {
    d.log.Println("Notifier waiting for handler init.")
    //case <-h.closed no need, won't fail when setup handler
    select {
    case <-h.ready:
        d.log.Println("Handler init done, Notifier spawning.")
        h.setupNotifier()
    }
}

// ListenHandlers listens on a passed port and upon accepting the incoming connection,
// adds the handler to deal with it
func (d *Dispatcher) ListenHandlers(port string) {
	var err error
	err = d.p.tg.Add()
	if err != nil {
		// If this goroutine is not run before shutdown starts, this
		// codeblock is reachable.
		return
	}

	d.ln, err = net.Listen("tcp", ":"+port)
	if err != nil {
		//d.log.Println(err)
		panic(err)
		// TODO: add error chan to report this
		//return
	}
	// fmt.Printf("Listening: %s\n", port)

	defer d.ln.Close()
	defer d.p.tg.Done()

	for {
		var conn net.Conn
		var err error
		select {
		case <-d.p.tg.StopChan():
			//fmt.Println("Closing listener")
			//d.ln.Close()
			//fmt.Println("Done closing listener")
			return
		default:
			conn, err = d.ln.Accept() // accept connection
			d.IncrementConnectionsOpened()
			if err != nil {
				//d.log.Println(err)
				continue
			}
		}
        go d.AddHandler(conn)
	}
}

// NotifyClients tells the dispatcher to notify all clients that the block has
// changed
func (d *Dispatcher) NotifyClients() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log.Printf("Block changed, notifying %d clients\n", len(d.handlers))
	for _, h := range d.handlers {
        //with new notifier it is no longer needed to be that large(20), 5 is for sure that won't block for long
        if len(h.notify) < numPendingNotifies {
		    h.notify <- true
        }
	}
}

// ClearJobAndNotifyClients clear all stale jobs and tells the dispatcher to notify all clients that the block has
// changed
func (d *Dispatcher) ClearJobAndNotifyClients() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.log.Printf("Work on new block, clear jobs and notifying %d clients\n", len(d.handlers))
	for _, h := range d.handlers {
		if h != nil && h.s != nil {
			if h.s.CurrentWorker == nil {
				// this will happen when handler init, session init,
				// no mining.authorize happen yet, so worker is nil,
				// at this time, no stratum notify ever happen, no need to clear or notify
				//d.log.Printf("Clear jobs and Notifying client: worker is nil\n")
				continue
			}
		} else {
			// this will happen when handler init, seesion is not
			//d.log.Printf("Clear jobs and Notifying client: handler or session nil\n")
			continue
		}
		h.s.clearJobs()
        //with new notifier it is no longer needed to be that large(20), 5 is for sure that won't block for long
        if len(h.notify) < numPendingNotifies {
		    h.notify <- true
        }
	}
}
