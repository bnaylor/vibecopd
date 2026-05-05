package setup

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

var httpClient = &http.Client{Timeout: 3 * time.Second}

func fetch(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func parseJSON(data []byte, dst any) error {
	return json.Unmarshal(data, dst)
}
