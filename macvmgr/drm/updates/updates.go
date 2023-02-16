package updates

import (
	"encoding/xml"
	"net/http"
)

type Updater struct {
	client *http.Client
}

func FetchAppcast() {
	m := make(map[string]any)
	xml.Unmarshal([]byte(""), &m)
}
