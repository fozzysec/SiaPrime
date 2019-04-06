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
    redisExpireTime    = 48 * 60 * 60 * time.Second
)

func (p *Pool) newDbConnection() error {
	dbc := *p.InternalSettings().PoolRedisConnection
	p.dbConnectionMu.Lock()
	defer p.dbConnectionMu.Unlock()
	var err error

    i := 0
    for _, conn := range p.redisdb {
        _, err = conn.Ping().Result()
        if err == nil {
            i++
        }
    }

    if i == len(DB) {
        return nil
    }

    for _, s := range DB {
        hosts := []string{}
        for _, host := range dbc["hosts"].([]string) {
            hosts = append(hosts, fmt.Sprintf("%s:%d", host, dbc["tables"].(map[string]interface{})[s].(int)))
        }

        fmt.Printf("try to connect redis: %s\n", s)
        p.redisdb[s], err = redis.NewClusterClient(&redis.ClusterOptions{
            Addrs:      hosts,
            Password:   dbc["pass"].(string),
        })
        if err != nil {
            fmt.Println(err)
            return err
        }
        _, err = p.redisdb[s].Ping().Result()
        if err != nil {
            fmt.Println(err)
            return err
        }
        fmt.Println("Connection successful.")
    }

    i = 0
    for _, conn := range p.redisdb {
        _, err = conn.Ping().Result()
        if err == nil {
            i++
        }
    }

    if i == len(DB) {
        return nil
    }

    return ErrConnectDB
}

func (p *Pool) closeAllDB() {
    for _, conn := range p.redisdb {
        conn.Close()
    }
}

// AddClientDB add user into accounts
func (p *Pool) AddClientDB(c *Client) error {
    id := p.newStratumID()()
    err := p.redisdb["accounts"].Set(c.Name(), id, 0).Err()
	if err != nil {
		return err
	}

	p.dblog.Printf("User %s account id is %d\n", c.Name(), id)
	c.SetID(id)

	return nil
}

// addWorkerDB inserts info to workers
func (c *Client) addWorkerDB(w *Worker) error {
    id := c.Pool().newStratumID()()
    err := c.Pool().redisdb["workers"].HMSet(
        fmt.Sprintf("%d.%d", c.GetID(), id),
        map[string]interface{} {
            "wallet":   c.Name(),
            "worker":   w.Name(),
            "time":     time.Now().Unix(),
            "pid":      c.pool.InternalSettings().PoolID,
            "version":  w.Session().clientVersion,
            "ip":       w.Session().remoteAddr,
        }).Err()
        err = c.Pool().redisdb["workers"].Persist(
            fmt.Sprintf("%d.%d", c.GetID(), id)).Err()
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
    } else if err != nil {
        fmt.Println(err)
        return nil, err
    }
    
    clientID, err = strconv.ParseUint(id, 10, 64)
    if err != nil {
        return nil, err
    }
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
    err := w.Parent().Pool().redisdb["workers"].Expire(
        fmt.Sprintf("%d.%d", w.Parent().GetID(), w.GetID()),
        redisExpireTime).Err()
	if err != nil {
		w.Parent().Pool().dblog.Printf("Error setting redis worker expire: %s\n", err)
		return err
	}
	return nil
}

func (p *Pool) DeleteAllWorkerRecords() error {
    conn := p.redisdb["workers"]
    var cursor uint64
    match := "*"
    count := int64(10)
    for {
        var keys []string
        var err error
        keys, cursor, err = conn.Scan(cursor, match, count).Result()
        if err != nil {
            fmt.Println(err)
            return err
        }
        for _, key := range keys {
            if conn.TTL(key).Val() < 0 {
                err = conn.Expire(key, redisExpireTime).Err()
                if err != nil {
                    return err
                }
            }
        }
        if cursor == 0 {
            break
        }
    }
    return nil
}
// DeleteAllWorkerRecords deletes all worker records associated with a pool.
// This should be used on pool startup and shutdown to ensure the database
// is clean and isn't storing any worker records for non-connected workers.
/*
func (p *Pool) DeleteAllWorkerRecords() error {
    err := p.redisdb["workers"].FlushDB().Err()
	if err != nil {
		p.dblog.Printf("Error deleting records: %s\n", err)
		return err
	}
	return nil
}*/

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
        strconv.FormatUint(uint64(bh), 10),
        map[string]interface{} {
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
    for _, share := range s.Shares() {
        err := redisdb.HMSet(
            fmt.Sprintf("%d.%d.%d.%d", client.GetID(), worker.GetID(), share.height, share.time.Unix()),
            map[string]interface{} {
                "valid":            share.valid,
                "difficulty":       share.difficulty,
                "reward":           share.reward,
                "block_difficulty": share.blockDifficulty,
                "share_reward":     share.shareReward,
                "share_diff":       share.shareDifficulty,
            }).Err()
        if err != nil {
            client.Pool().dblog.Println(err)
            return err
        }
    }
    return nil
}
