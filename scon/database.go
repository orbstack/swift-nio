package main

import (
	"bytes"
	"encoding/gob"
	"errors"

	"github.com/orbstack/macvirt/scon/types"
	"go.etcd.io/bbolt"
)

const (
	// v1: initial
	// v2: added container state machine (replaces running/deleting flags)
	dbVersion = 2

	bktMeta   = "meta"
	kmVersion = "version"

	bktState             = "state"
	ksLastContainerID    = "lastContainerID"
	ksDefaultContainerID = "defaultContainerID"
	ksDefaultUsername    = "defaultUsername"
	ksDnsLastQueries     = "dnsLastQueries"

	bktContainers = "containers"

	containerIDLastUsed = "01GRWR24S00000000LAST0USED"
)

type DnsLastQueries struct {
	Queries map[string]mdnsQueryInfo
}

var (
	ErrKeyNotFound = errors.New("key not found")
)

type Database struct {
	db *bbolt.DB
}

func OpenDatabase(path string) (*Database, error) {
	boltDb, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}

	db := &Database{
		db: boltDb,
	}
	err = db.init()
	if err != nil {
		return nil, err
	}

	return db, nil
}

func (db *Database) Close() error {
	return db.db.Close()
}

func (db *Database) init() error {
	err := db.db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bktContainers))
		if err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists([]byte(bktState))
		if err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists([]byte(bktMeta))
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	ver := db.getVersion()
	// if new or default
	if ver == dbVersion {
		err = db.setVersion(dbVersion)
		if err != nil {
			return err
		}
		return nil
	}

	// migrations
	if ver == 1 {
		err = db.migrate1to2()
		if err != nil {
			return err
		}

		// set version
		err = db.setVersion(2)
		if err != nil {
			return err
		}
		return nil
	}

	return nil
}

func (db *Database) migrate1to2() error {
	// new container records: running+deleting -> state machine
	return db.db.Update(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(bktContainers))
		if bkt == nil {
			return bbolt.ErrBucketNotFound
		}
		return bkt.ForEach(func(k, v []byte) error {
			var containerV1 types.ContainerRecordV1
			err := gobDecode(v, &containerV1)
			if err != nil {
				return err
			}

			containerV2 := types.ContainerRecord{
				ID:    containerV1.ID,
				Name:  containerV1.Name,
				Image: containerV1.Image,

				Config: types.MachineConfig{
					Isolated: containerV1.Isolated,
				},

				Builtin: containerV1.Builtin,
				State:   types.ContainerStateStopped,
			}
			if containerV1.Running {
				containerV2.State = types.ContainerStateRunning
			} else if containerV1.Deleting {
				containerV2.State = types.ContainerStateDeleting
			}

			data, err := gobEncode(&containerV2)
			if err != nil {
				return err
			}
			return bkt.Put([]byte(k), data)
		})
	})
}

func (db *Database) getVersion() int {
	ver, err := getSimpleGob[int](db, bktMeta, kmVersion)
	if err != nil {
		return dbVersion
	}
	return ver
}

func (db *Database) setVersion(ver int) error {
	err := setSimpleGob(db, bktMeta, kmVersion, ver)
	if err != nil {
		return err
	}
	return nil
}

func (db *Database) getSimpleStr(bucket, key string) (string, error) {
	var val string
	err := db.db.View(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(bucket))
		if bkt == nil {
			return bbolt.ErrBucketNotFound
		}
		data := bkt.Get([]byte(key))
		if data == nil {
			return ErrKeyNotFound
		}
		val = string(data)
		return nil
	})
	return val, err
}

func (db *Database) setSimpleStr(bucket, key, val string) error {
	return db.db.Update(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(bucket))
		if bkt == nil {
			return bbolt.ErrBucketNotFound
		}
		return bkt.Put([]byte(key), []byte(val))
	})
}

func (db *Database) deleteSimple(bucket, key string) error {
	return db.db.Update(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(bucket))
		if bkt == nil {
			return bbolt.ErrBucketNotFound
		}
		return bkt.Delete([]byte(key))
	})
}

func getSimpleGob[T any](db *Database, bucket, key string) (T, error) {
	var val T
	err := db.db.View(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(bucket))
		if bkt == nil {
			return bbolt.ErrBucketNotFound
		}
		data := bkt.Get([]byte(key))
		if data == nil {
			return ErrKeyNotFound
		}
		return gobDecode(data, &val)
	})
	return val, err
}

func setSimpleGob[T any](db *Database, bucket, key string, val T) error {
	return db.db.Update(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(bucket))
		if bkt == nil {
			return bbolt.ErrBucketNotFound
		}
		data, err := gobEncode(val)
		if err != nil {
			return err
		}
		return bkt.Put([]byte(key), data)
	})
}

func gobDecode[T any](data []byte, val T) error {
	dec := gob.NewDecoder(bytes.NewReader(data))
	return dec.Decode(val)
}

func gobEncode[T any](val T) ([]byte, error) {
	buf := new(bytes.Buffer)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(val)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (db *Database) GetLastContainerID() (string, error) {
	return db.getSimpleStr(bktState, ksLastContainerID)
}

func (db *Database) SetLastContainerID(id string) error {
	return db.setSimpleStr(bktState, ksLastContainerID, id)
}

func (db *Database) GetDefaultContainerID() (string, error) {
	return db.getSimpleStr(bktState, ksDefaultContainerID)
}

func (db *Database) SetDefaultContainerID(id string) error {
	return db.setSimpleStr(bktState, ksDefaultContainerID, id)
}

func (db *Database) GetDefaultUsername() (string, error) {
	return db.getSimpleStr(bktState, ksDefaultUsername)
}

func (db *Database) SetDefaultUsername(username string) error {
	return db.setSimpleStr(bktState, ksDefaultUsername, username)
}

func (db *Database) GetDnsRecentQueries() (map[string]mdnsQueryInfo, error) {
	v, err := getSimpleGob[DnsLastQueries](db, bktState, ksDnsLastQueries)
	if err != nil {
		return nil, err
	}
	return v.Queries, nil
}

func (db *Database) SetDnsRecentQueries(recentQueries map[string]mdnsQueryInfo) error {
	return setSimpleGob(db, bktState, ksDnsLastQueries, DnsLastQueries{
		Queries: recentQueries,
	})
}

func (db *Database) GetContainer(id string) (*types.ContainerRecord, error) {
	return getSimpleGob[*types.ContainerRecord](db, bktContainers, id)
}

func (db *Database) SetContainer(id string, container *types.ContainerRecord) error {
	return setSimpleGob(db, bktContainers, id, container)
}

func (db *Database) DeleteContainer(id string) error {
	return db.deleteSimple(bktContainers, id)
}

func (db *Database) GetContainers() ([]*types.ContainerRecord, error) {
	var containers []*types.ContainerRecord
	err := db.db.View(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(bktContainers))
		if bkt == nil {
			return bbolt.ErrBucketNotFound
		}
		return bkt.ForEach(func(k, v []byte) error {
			var container types.ContainerRecord
			err := gobDecode(v, &container)
			if err != nil {
				return err
			}
			containers = append(containers, &container)
			return nil
		})
	})
	return containers, err
}
