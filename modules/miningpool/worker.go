package pool

import (
	"time"
    "unsafe"

    "sync"
    "sync/atomic"

	"SiaPrime/persist"
)

// A WorkerRecord is used to track worker information in memory
type WorkerRecord struct {
	name            string
	workerID        int64
	shareDifficulty float64
	parent          *Client
}

// A Worker is an instance of one miner.  A Client often represents a user and the
// worker represents a single miner.  There is a one to many client worker relationship
type Worker struct {
	mu sync.RWMutex
	wr WorkerRecord
	s  *Session
	// utility
	log *persist.Logger
}

func newWorker(c *Client, name string, s *Session) (*Worker, error) {
	p := c.Pool()
	// id := p.newStratumID()
	w := &Worker{
		wr: WorkerRecord{
			// workerID: id(),
			name:   name,
			parent: c,
		},
	}
    w.SetSession(s)

    w.log = p.log
	return w, nil
}

func (w *Worker) printID() string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return ssPrintID(w.GetID())
}

// Name return the worker's name, typically a wallet address
func (w *Worker) Name() string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	return w.wr.name
}

// SetName sets the worker's name
func (w *Worker) SetName(n string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.wr.name = n
}

func (w *Worker) SetID(id int64) {
    atomic.StoreInt64(&w.wr.workerID, id)
}

func (w *Worker) GetID() int64 {
    return atomic.LoadInt64(&w.wr.workerID)
}

// Parent returns the worker's client
func (w *Worker) Parent() *Client {
	return (*Client)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&w.wr.parent))))
}

// SetParent sets the worker's client
func (w *Worker) SetParent(p *Client) {
    atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&w.wr.parent)), unsafe.Pointer(p))
}

// Session returns the tcp session associated with the worker
func (w *Worker) Session() *Session {
    return (*Session)(atomic.LoadPointer((*unsafe.Pointer)(unsafe.Pointer(&w.s))))
}

// SetSession sets the tcp session associated with the worker
func (w *Worker) SetSession(s *Session) {
    atomic.StorePointer((*unsafe.Pointer)(unsafe.Pointer(&w.s)), unsafe.Pointer(s))
}

// IncrementShares creates a new share according to current session difficulty
// for the worker to work on
func (w *Worker) IncrementShares(sessionDifficulty float64, reward float64) {
	p := w.s.GetClient().pool
	cbid := p.cs.CurrentBlock().ID()
	blockTarget, _ := p.cs.ChildTarget(cbid)
	blockDifficulty, _ := blockTarget.Difficulty().Uint64()

	sessionTarget, _ := difficultyToTarget(sessionDifficulty)
	siaSessionDifficulty, _ := sessionTarget.Difficulty().Uint64()
	shareRatio := caculateRewardRatio(sessionTarget.Difficulty().Big(), blockTarget.Difficulty().Big())
	shareReward := shareRatio * reward
	// w.log.Printf("shareRatio: %f, shareReward: %f", shareRatio, shareReward)

	share := &Share{
		userid:          w.Parent().cr.clientID,
		workerid:        w.GetID(),
		height:          int64(p.cs.Height()) + 1,
		valid:           true,
		difficulty:      sessionDifficulty,
		shareDifficulty: float64(siaSessionDifficulty),
		reward:          reward,
		blockDifficulty: blockDifficulty,
		shareReward:     shareReward,
		time:            time.Now(),
	}

	w.s.Shift().IncrementShares(share)
}

// IncrementInvalidShares adds a record of an invalid share submission
func (w *Worker) IncrementInvalidShares() {
	w.s.Shift().IncrementInvalid()
}

// SetLastShareTime specifies the last time a share was submitted during the
// current shift
func (w *Worker) SetLastShareTime(t time.Time) {
	w.s.Shift().SetLastShareTime(t)
}

// LastShareTime returns the last time a share was submitted during the
// current shift
func (w *Worker) LastShareTime() time.Time {
	return w.s.Shift().LastShareTime()
}

// CurrentDifficulty returns the average difficulty of all instances of this worker
func (w *Worker) CurrentDifficulty() float64 {
	pool := w.wr.parent.Pool()
	d := pool.dispatcher
	d.mu.Lock()
	defer d.mu.Unlock()
	workerCount := uint64(0)
	currentDiff := float64(0.0)
	for _, h := range d.handlers {
		if h.s.GetClient() != nil && h.s.GetClient().Name() == w.Parent().Name() && h.s.GetCurrentWorker().Name() == w.Name() {
			currentDiff += h.s.CurrentDifficulty()
			workerCount++
		}
	}
	if workerCount == 0 {
		return 0.0
	}
	return currentDiff / float64(workerCount)
}

// Online checks if the worker has a tcp session associated with it
func (w *Worker) Online() bool {
	return w.s != nil
}
