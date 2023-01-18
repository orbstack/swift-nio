package main

import (
	"net/http"
)

type SconServer struct {
	
}

func runSconServer() error {
	mux := http.NewServeMux()
	return http.ListenAndServe(":8080", mux)
}
