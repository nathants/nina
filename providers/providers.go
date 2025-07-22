package providers

import (
	"net/http"
	"sync"
	"time"
)

var (
	LongTimeoutClient  *http.Client
	ShortTimeoutClient *http.Client
	httpClientsOnce    sync.Once
)

func InitAllHTTPClients() {
	httpClientsOnce.Do(func() {
		transport := &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		}
		LongTimeoutClient = &http.Client{
			Timeout:   15 * time.Minute,
			Transport: transport,
		}

		ShortTimeoutClient = &http.Client{
			Timeout:   3 * time.Minute,
			Transport: transport,
		}
	})
}
