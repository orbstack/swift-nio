package main

import (
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"go.etcd.io/bbolt"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	db, err := bbolt.Open("my.db", 0600, nil)
	check(err)
	defer db.Close()

	rand.Seed(42)
	var counter atomic.Int64
	go func() {
		for range time.Tick(time.Second) {
			fmt.Println(counter.Swap(0), "txn/s")
		}
	}()
	for {
		// randomly add, delete, or update a key
		r := rand.Intn(4)
		switch r {
		case 0, 3:
			// add
			err = db.Update(func(tx *bbolt.Tx) error {
				b, err := tx.CreateBucketIfNotExists([]byte("mybucket"))
				check(err)
				randKey := make([]byte, 16)
				rand.Read(randKey)
				randVal := make([]byte, 16)
				rand.Read(randVal)
				err = b.Put(randKey, randVal)
				check(err)
				return nil
			})
			check(err)
		case 1:
			// delete
			err = db.Update(func(tx *bbolt.Tx) error {
				b := tx.Bucket([]byte("mybucket"))
				if b == nil {
					return nil
				}
				// get a random key
				c := b.Cursor()
				k, _ := c.First()
				for i := 0; i < rand.Intn(10); i++ {
					// avoid nil
					nextK, _ := c.Next()
					if nextK != nil {
						k = nextK
					}
				}
				if k == nil {
					return nil
				}
				err = b.Delete(k)
				check(err)
				return nil
			})
			check(err)
		case 2:
			// update
			err = db.Update(func(tx *bbolt.Tx) error {
				b := tx.Bucket([]byte("mybucket"))
				if b == nil {
					return nil
				}
				// get a random key
				c := b.Cursor()
				k, _ := c.First()
				for i := 0; i < rand.Intn(10); i++ {
					// avoid nil
					nextK, _ := c.Next()
					if nextK != nil {
						k = nextK
					}
				}
				if k == nil {
					return nil
				}
				randVal := make([]byte, 16)
				rand.Read(randVal)
				err = b.Put(k, randVal)
				check(err)
				return nil

			})
			check(err)
		}
		counter.Add(1)
	}
}
