#!/usr/bin/env bash

function emit_commands {
    # echo "PRAGMA journal_mode=wal;"
    echo "******** TEST" >&2
    echo "SELECT * FROM test;"
    echo "******** TEST2" >&2
    echo "SELECT * FROM test2;"
}

emit_commands | sqlite3 sqlite.db
