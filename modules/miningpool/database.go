package pool

import (
	//"database/sql"
    "github.com/go-redis/redis"
	//"context"
	"errors"
	"fmt"
    "strconv"
	"time"

	"SiaPrime/types"
)

var (
	// ErrDuplicateUserInDifferentCoin is an error when a address used in
	// different coin
	//ErrDuplicateUserInDifferentCoin = errors.New("duplicate user in different coin, you need use a different address")
	// ErrNoUsernameInDatabase is an error when can't find a username in db
	ErrNoUsernameInDatabase = errors.New("user is not found in db")
	// ErrCreateClient is an error when can't create a new client
	ErrCreateClient = errors.New("Error when creating a new client")
	ErrQueryTimeout = errors.New("DB query timeout")
    ErrConnectDB = errors.New("error in connecting redis")
    DB = []string{"accounts", "workers", "shares", "blocks"}
)

const (
	sqlReconnectRetry  = 6
	sqlRetryDelay      = 10
	sqlQueryTimeout    = 5
)

func (p *Pool) newDbConnection() error {
	dbc := p.InternalSettings().PoolDBConnection
	p.dbConnectionMu.Lock()
	defer p.dbConnectionMu.Unlock()
	var err error

    i := 0
    for _, conn := range p.redisdb {
        err = conn.Ping().Err()
        if err == nil {
            i++
        }
    }

    if i == len(DB) {
        return nil
    }

    for index, s := range DB {
        for i := 0; i < sqlReconnectRetry; i++ {
            fmt.Printf("try to connect redis: %s\n", s)
            p.redisdb[s] = redis.NewClient(&redis.Options{
                Addr:       dbc["addr"],
                Password:   dbc["pass"],
                DB:         index,
            })

            err = p.redisdb[s].Ping().Err()
            if err != nil {
                time.Sleep(sqlRetryDelay * time.Second)
                continue
            }
            fmt.Println("Connection successful.")
            break
        }
    }

    for _, conn := range p.redisdb {
        err = conn.Ping().Err()
        if err == nil {
            i++
        }
    }

    if i == len(DB) {
        return nil
    }

    return ErrConnectDB
}

// AddClientDB add user into accounts
func (p *Pool) AddClientDB(c *Client) error {
    id := p.newStratumID()
    err := p.redisdb["accounts"].Set(c.Name(), id(), 0).Err()
	if err != nil {
		return err
	}

	p.dblog.Printf("User %s account id is %d\n", c.Name(), id())
	c.SetID(id)

	return nil
}

// addWorkerDB inserts info to workers
func (c *Client) addWorkerDB(w *Worker) error {
    id := c.pool.newStratumID()
    c.pool.redisdb["workers"].HMSet(
        fmt.Sprintf("%s.%s", c.Name(), w.Name()),
        map[string]string{
            "id":       id(),
            "time":     time.Now().Unix(),
            "pid":      c.pool.InternalSettings().PoolID,
            "version":  w.Session().clientVersion,
            "ip":       w.Session().remoteAddr,
        }).Err()
	if err != nil {
		return err
	}
	w.SetID(id)
	return nil
}

// FindClientDB find user in accounts
func (p *Pool) FindClientDB(name string) (*Client, error) {
	var clientID uint64
    var err error
	c := p.Client(name)
	// if it's in memory, just return a pointer to the copy in memory
	if c != nil {
		return c, nil
	}

    id, err := p.redisdb["accounts"].Get(name).Result()
    if err == redis.Nil {
        return nil, ErrNoUsernameInDatabase
    }
    clientID = strconv.ParseInt(id, 10, 64)
	// client was in database but not in memory -
	// find workers and connect them to the in memory copy
	c, err = newClient(p, name)
	if err != nil {
		p.dblog.Printf("Error when creating a new client %s: %s\n", name, err)
		return nil, ErrCreateClient
	}
	var wallet types.UnlockHash
	wallet.LoadString(name)
	c.SetWallet(wallet)
	c.SetID(clientID)

	return c, nil
}

func (w *Worker) deleteWorkerRecord() error {

    err := w.Parent().pool.redisdb["workers"].Del(
        fmt.Sprintf(
            "%s.%s",
            w.Parent().Name(),
            w.Name())).Err()
	if err != nil {
		w.wr.parent.pool.dblog.Printf("Error deleting record: %s\n", err)
		return err
	}
	return nil
}

// DeleteAllWorkerRecords deletes all worker records associated with a pool.
// This should be used on pool startup and shutdown to ensure the database
// is clean and isn't storing any worker records for non-connected workers.
func (p *Pool) DeleteAllWorkerRecords() error {
    err := w.Parent().pool.redisdb["workers"].FlushDB().Err()
	if err != nil {
		p.dblog.Printf("Error deleting records: %s\n", err)
		return err
	}
	return nil
}

// addFoundBlock add founded block to yiimp blocks table
func (w *Worker) addFoundBlock(b *types.Block) error {
	pool := w.Parent().Pool()
	bh := pool.persist.GetBlockHeight()
	//w.log.Printf("New block to mine on %d\n", uint64(bh)+1)
	// reward := b.CalculateSubsidy(bh).String()
	pool.blockFoundMu.Lock()
	defer pool.blockFoundMu.Unlock()
	timeStamp := time.Now().Unix()
	currentTarget, _ := pool.cs.ChildTarget(b.ID())
	difficulty, _ := currentTarget.Difficulty().Uint64() // TODO: maybe should use parent ChildTarget

    err := pool.redisdb["blocks"].HMSet(
        strconv.FormatInt(bh, 10),
        map[string]string{
            "blockhash":    b.ID().String(),
            "user":         w.Parent().Name(),
            "worker":       w.Name(),
            "category":     "new",
            "difficulty":   difficulty,
            "time":         timeStamp,
        }).Err()
	if err != nil {
		return err
	}
	return nil
}

// SaveShift periodically saves the shares for a given worker to the db
func (s *Shift) SaveShift() error {
    if len(s.Shares()) == 0 {
        return nil
    }

    worker := s.worker
    client := worker.Parent()
    redisdb := client.Pool().redisdb["shares"]
    for i, share := range s.Shares() {
        err := redisdb.HMSet(
            fmt.Sprintf("%d.%d.%d", worker.GetID(), client.GetID(), share.time.Unix()),
            map[string]string{
                "valid":            share.valid,
                "difficulty":       share.difficulty,
                "reward":           share.reward,
                "block_difficulty": share.blockDifficulty,
                "share_reward":     share.shareReward,
                "share_diff":       share.shareDifficulty,
            }).Err()
        if err != nil {
            worker.wr.parent.pool.dblog.Println(err)
            err = pool.newDbConnection()
            if err != nil {
                worker.wr.parent.pool.dblog.Println(err)
                return err
            }
            err2 := redisdb.HMSet(
                fmt.Sprintf("%d.%d.%d", worker.GetID(), client.GetID(), share.time.Unix()),
                map[string]string{
                    "valid":            share.valid,
                    "difficulty":       share.difficulty,
                    "reward":           share.reward,
                    "block_difficulty": share.blockDifficulty,
                    "share_reward":     share.shareReward,
                    "share_diff":       share.shareDifficulty,
                }).Err()
            if err2 != nil {
                worker.wr.parent.pool.dblog.Println(err2)
                return err2
            }
        }
    }
    return nil
}
