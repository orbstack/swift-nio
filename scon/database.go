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
	// v3: added ssh host keys
	dbVersion = 3

	bktMeta       = "meta"
	bktState      = "state"
	bktContainers = "containers"

	oldDefaultHostKeyEd25519 = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACAgEJD3oK7ddXQktDsupy91mk85nbFM12Y6srQ0ujq4oAAAAKDLA5G2ywOR
tgAAAAtzc2gtZWQyNTUxOQAAACAgEJD3oK7ddXQktDsupy91mk85nbFM12Y6srQ0ujq4oA
AAAEAdZQRbxMDW6DaGP2YY8yxby24cwECktHygG1dGxHmuFiAQkPegrt11dCS0Oy6nL3Wa
TzmdsUzXZjqytDS6OrigAAAAFmRyYWdvbkBhbmRyb21lZGEubG9jYWwBAgMEBQYH
-----END OPENSSH PRIVATE KEY-----`
	oldDefaultHostKeyECDSA = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAaAAAABNlY2RzYS
1zaGEyLW5pc3RwMjU2AAAACG5pc3RwMjU2AAAAQQSo65hrIeTFpS/ZFiZNzAkPO9zs9GzV
GbZgYtsv8wJ19AgMR8LrYnGNK3cgYVJWnXe5WXjK8IZwxF/jT9cL4YO0AAAAqJDz+WiQ8/
loAAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBKjrmGsh5MWlL9kW
Jk3MCQ873Oz0bNUZtmBi2y/zAnX0CAxHwuticY0rdyBhUladd7lZeMrwhnDEX+NP1wvhg7
QAAAAhALqjXlpenZU0ClqZAG4ypGXwwY0N2/O1uycE8O5Zt7q1AAAACXJvb3RAdWdlbgEC
AwQFBg==
-----END OPENSSH PRIVATE KEY-----`
)

type dbKey struct {
	bucket string
	key    string
}

var (
	kmVersion = dbKey{bktMeta, "version"}

	ksDefaultContainerID = dbKey{bktState, "defaultContainerID"}
	ksDnsLastQueries     = dbKey{bktState, "dnsLastQueries"}
	ksSshHostKeyIsLegacy = dbKey{bktState, "sshHostKey.isLegacy"}
	ksSshHostKeyEd25519  = dbKey{bktState, "sshHostKey.ed25519"}
	ksSshHostKeyECDSA    = dbKey{bktState, "sshHostKey.ecdsa"}
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
		ver = 2
	}

	if ver == 2 {
		err = db.migrate2to3()
		if err != nil {
			return err
		}
		ver = 3
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
		err := bkt.ForEach(func(k, v []byte) error {
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
		if err != nil {
			return err
		}

		err = setSimpleGobWithTx(tx, kmVersion, 2)
		if err != nil {
			return err
		}

		return nil
	})
}

func (db *Database) migrate2to3() error {
	// when upgrading from v2, save old hard-coded ssh host keys and set flag
	// this preserves known_hosts compat but also sets a flag so that we know the keys are insecure and must only be used locally. if we later add a setting to allow exposing the SSH server to 0.0.0.0, then we know that keys need to be regenerated if the flag is set.
	return db.db.Update(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(bktState))
		if bkt == nil {
			return bbolt.ErrBucketNotFound
		}

		err := setSimpleGobWithTx(tx, ksSshHostKeyIsLegacy, true)
		if err != nil {
			return err
		}

		err = bkt.Put([]byte(ksSshHostKeyEd25519.key), []byte(oldDefaultHostKeyEd25519))
		if err != nil {
			return err
		}

		err = bkt.Put([]byte(ksSshHostKeyECDSA.key), []byte(oldDefaultHostKeyECDSA))
		if err != nil {
			return err
		}

		err = setSimpleGobWithTx(tx, kmVersion, 3)
		if err != nil {
			return err
		}

		return nil
	})
}

func (db *Database) getVersion() int {
	ver, err := getSimpleGob[int](db, kmVersion)
	if err != nil {
		return dbVersion
	}
	return ver
}

func (db *Database) setVersion(ver int) error {
	err := setSimpleGob(db, kmVersion, ver)
	if err != nil {
		return err
	}
	return nil
}

func (db *Database) getSimpleStr(dbKey dbKey) (string, error) {
	var val string
	err := db.db.View(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(dbKey.bucket))
		if bkt == nil {
			return bbolt.ErrBucketNotFound
		}
		data := bkt.Get([]byte(dbKey.key))
		if data == nil {
			return ErrKeyNotFound
		}
		val = string(data)
		return nil
	})
	return val, err
}

func (db *Database) setSimpleStr(dbKey dbKey, val string) error {
	return db.db.Update(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(dbKey.bucket))
		if bkt == nil {
			return bbolt.ErrBucketNotFound
		}
		return bkt.Put([]byte(dbKey.key), []byte(val))
	})
}

func (db *Database) deleteSimple(dbKey dbKey) error {
	return db.db.Update(func(tx *bbolt.Tx) error {
		bkt := tx.Bucket([]byte(dbKey.bucket))
		if bkt == nil {
			return bbolt.ErrBucketNotFound
		}
		return bkt.Delete([]byte(dbKey.key))
	})
}

func getSimpleGobWithTx[T any](tx *bbolt.Tx, dbKey dbKey) (T, error) {
	var val T
	bkt := tx.Bucket([]byte(dbKey.bucket))
	if bkt == nil {
		return val, bbolt.ErrBucketNotFound
	}
	data := bkt.Get([]byte(dbKey.key))
	if data == nil {
		return val, ErrKeyNotFound
	}
	err := gobDecode(data, &val)
	return val, err
}

func getSimpleGob[T any](db *Database, dbKey dbKey) (T, error) {
	var val T
	err := db.db.View(func(tx *bbolt.Tx) error {
		var err error
		val, err = getSimpleGobWithTx[T](tx, dbKey)
		return err
	})
	return val, err
}

func setSimpleGobWithTx[T any](tx *bbolt.Tx, dbKey dbKey, val T) error {
	bkt := tx.Bucket([]byte(dbKey.bucket))
	if bkt == nil {
		return bbolt.ErrBucketNotFound
	}
	data, err := gobEncode(val)
	if err != nil {
		return err
	}
	return bkt.Put([]byte(dbKey.key), data)
}

func setSimpleGob[T any](db *Database, dbKey dbKey, val T) error {
	return db.db.Update(func(tx *bbolt.Tx) error {
		return setSimpleGobWithTx(tx, dbKey, val)
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

func (db *Database) GetDefaultContainerID() (string, error) {
	return db.getSimpleStr(ksDefaultContainerID)
}

func (db *Database) SetDefaultContainerID(id string) error {
	return db.setSimpleStr(ksDefaultContainerID, id)
}

func (db *Database) GetDnsRecentQueries() (map[string]mdnsQueryInfo, error) {
	v, err := getSimpleGob[DnsLastQueries](db, ksDnsLastQueries)
	if err != nil {
		return nil, err
	}
	return v.Queries, nil
}

func (db *Database) SetDnsRecentQueries(recentQueries map[string]mdnsQueryInfo) error {
	return setSimpleGob(db, ksDnsLastQueries, DnsLastQueries{
		Queries: recentQueries,
	})
}

func (db *Database) GetContainer(id string) (*types.ContainerRecord, error) {
	return getSimpleGob[*types.ContainerRecord](db, dbKey{bktContainers, id})
}

func (db *Database) SetContainer(id string, container *types.ContainerRecord) error {
	return setSimpleGob(db, dbKey{bktContainers, id}, container)
}

func (db *Database) DeleteContainer(id string) error {
	return db.deleteSimple(dbKey{bktContainers, id})
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
