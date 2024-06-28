package main

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"go.etcd.io/bbolt"
)

const (
	numRows  = 300
	numRows2 = 5

	alwaysGrow = true
)

func main() {
	db, err := bbolt.Open("bbolt.db", 0600, nil)
	if err != nil {
		panic(err)
	}

	for round := 0; ; round++ {
		err := db.Update(func(tx *bbolt.Tx) error {
			testBucket, err := tx.CreateBucketIfNotExists([]byte("test"))
			if err != nil {
				return err
			}

			for i := 0; i < numRows; i++ {
				dateStr := []byte(time.Now().Format(time.RFC3339Nano))
				extraKey := ""
				if alwaysGrow {
					extraKey = fmt.Sprintf(".%d", rand.Uint64())
				}
				err = testBucket.Put([]byte(fmt.Sprintf("%d%s.date", i, extraKey)), dateStr)
				if err != nil {
					return err
				}
				err = testBucket.Put([]byte(fmt.Sprintf("%d%s.round", i, extraKey)), []byte(fmt.Sprintf("%d", round)))
				if err != nil {
					return err
				}
			}

			// delete all of test2
			err = tx.DeleteBucket([]byte("test2"))
			if err != nil && !errors.Is(err, bbolt.ErrBucketNotFound) {
				return err
			}
			// create test2
			testBucket2, err := tx.CreateBucket([]byte("test2"))
			if err != nil {
				return err
			}
			// insert 5 rows into test2
			for i := 0; i < numRows2; i++ {
				dateStr := []byte(time.Now().Format(time.RFC3339Nano))
				err = testBucket2.Put([]byte(fmt.Sprintf("%d.date", i)), dateStr)
				if err != nil {
					return err
				}
			}

			return nil
		})
		if err != nil {
			panic(err)
		}
	}
}
