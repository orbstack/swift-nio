#!/usr/bin/env bash

num_rows=300
num_rows2=5

GNU_DATE=date
if [[ $(uname) == "Darwin" ]]; then
    GNU_DATE=gdate
fi

function emit_commands {
    # this tends to be *MORE* robust.
    # echo "PRAGMA journal_mode=wal;"

    echo "CREATE TABLE IF NOT EXISTS test (id INT PRIMARY KEY, date_str TEXT, date_ns INT, round INT);"
    echo "CREATE INDEX IF NOT EXISTS test_date_ns_idx ON test (date_ns);"
    echo "CREATE TABLE IF NOT EXISTS test2 (id INT PRIMARY KEY, date_str TEXT);"
    round=0
    while true; do
        echo "round: $round" >&2
        round=$((round + 1))
        echo "BEGIN TRANSACTION;"
        date_str="$(date)"
        for i in $(seq 1 $num_rows); do
            date_ns="$($GNU_DATE +%N)"
            echo "INSERT INTO test (id, date_str, date_ns, round) VALUES ($i, '$date_str', $date_ns, $round) ON CONFLICT(id) DO UPDATE SET date_str=excluded.date_str, date_ns=excluded.date_ns, round=excluded.round;"
        done

        # delete all of test2
        echo "DELETE FROM test2;"
        # insert 5 rows into test2
        for i in $(seq 1 $num_rows2); do
            echo "INSERT INTO test2 (id, date_str) VALUES ($i, '$date_str') ON CONFLICT(id) DO UPDATE SET date_str=excluded.date_str;"
        done

        echo "COMMIT;"
    done
}

emit_commands | sqlite3 sqlite.db
