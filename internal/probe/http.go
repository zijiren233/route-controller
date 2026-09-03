// Package probe checks Cilium health endpoints used to select Service CIDR next hops.
package probe

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"
)

const maximumResponseBytes = 4 << 10

type Prober interface {
	Healthy(ctx context.Context, address netip.Addr) bool
}

type HTTP struct {
	client *http.Client
	port   int
}

func NewHTTP(timeout time.Duration, port int) *HTTP {
	return &HTTP{
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DisableKeepAlives: true,
			},
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		port: port,
	}
}

func (probe *HTTP) Healthy(ctx context.Context, address netip.Addr) bool {
	endpoint := "http://" + net.JoinHostPort(address.String(), strconv.Itoa(probe.port)) + "/hello"

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}

	response, err := probe.client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()

	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maximumResponseBytes))

	return response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
}

func (probe *HTTP) String() string {
	return "Cilium HTTP health probe on port " + strconv.Itoa(probe.port)
}
