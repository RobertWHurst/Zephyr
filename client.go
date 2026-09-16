package zephyr

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/RobertWHurst/navaros"
	"github.com/telemetryos/go-debug/debug"
)

var (
	clientRequestDebug = debug.Bind("zephyr:client:request")
	clientServeDebug   = debug.Bind("zephyr:client:serve")
)

// Client can make requests to services.
type Client struct {
	Transport Transport
}

// NewClient creates a new client with the given transport.
func NewClient(transport Transport) *Client {
	return &Client{
		Transport: transport,
	}
}

// Service returns a ServiceClient for the given service name.
func (c *Client) Service(name string) *ServiceClient {
	return &ServiceClient{
		Client: c,
		Name:   name,
	}
}

// ServiceClient is a client that can make requests to a specific service.
// To get a ServiceClient for a specific service, call Service on a Client.
type ServiceClient struct {
	*Client
	Name string
}

// Do sends an HTTP request to the service.
func (c *ServiceClient) Do(req *http.Request) (*http.Response, error) {
	clientRequestDebug.Tracef("Request to %s: %s %s", c.Name, req.Method, req.URL.Path)

	responseRecorder := httptest.NewRecorder()
	if err := c.Transport.Dispatch(c.Name, responseRecorder, req); err != nil {
		clientRequestDebug.Tracef("Request to %s failed: %v", c.Name, err)
		return nil, err
	}

	response := responseRecorder.Result()
	clientRequestDebug.Tracef("Response from %s: status=%d, contentLength=%d",
		c.Name, response.StatusCode, response.ContentLength)

	return response, nil
}

// Get sends a GET request to the service.
func (c *ServiceClient) Get(servicePath string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, servicePath, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Head sends a HEAD request to the service.
func (c *ServiceClient) Head(servicePath string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodHead, servicePath, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Post sends a POST request to the service.
func (c *ServiceClient) Post(servicePath string, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, servicePath, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

// PostForm sends a POST request with form data to the service.
func (c *ServiceClient) PostForm(servicePath string, data url.Values) (*http.Response, error) {
	return c.Post(servicePath, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
}

// ServeHTTP implements http.Handler. It allows a ServiceClient to proxy
// requests to a service.
func (c *ServiceClient) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	clientServeDebug.Tracef("ServiceClient %s handling HTTP request: %s %s", c.Name, r.Method, r.URL.Path)

	if err := c.Transport.Dispatch(c.Name, w, r); err != nil {
		clientServeDebug.Tracef("Error dispatching request to %s: %v", c.Name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

	clientServeDebug.Tracef("Completed handling request to %s: %s %s", c.Name, r.Method, r.URL.Path)
}

// Handle implements navaros.Handler. It allows a ServiceClient to proxy
// requests to a service.
func (c *ServiceClient) Handle(ctx *navaros.Context) {
	c.ServeHTTP(ctx.ResponseWriter(), ctx.Request())
}
