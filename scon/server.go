package main

import (
	"net/http"
)

type SconServer struct {
	m *ConManager
}

func runSconServer(m *ConManager) error {
	mux := http.NewServeMux()
	return http.ListenAndServe(":8080", mux)
}
