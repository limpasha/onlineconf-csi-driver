package main

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"time"
)

const (
	defaultHookConnectTimeout = time.Second
	defaultHookRequestTimeout = time.Second
)

var client = &http.Client{
	Timeout: defaultHookRequestTimeout,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		DialContext:     (&net.Dialer{Timeout: defaultHookConnectTimeout}).DialContext,
	},
}

func callHookURL(url string) (string, error) {

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("Error creating request: %w", err)
	}

	res, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("Error calling: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Bad response (%d): %w", res.StatusCode, err)
	}

	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("Error reading body: %w", err)
	}

	return string(body), nil
}
