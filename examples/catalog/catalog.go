// Package catalog demonstrates exported functional options and language names.
package catalog

import "github.com/sambhav/gobridge"

// Catalog owns an immutable configured endpoint.
//
//gobridge:python CatalogClient
//gobridge:ts CatalogApi
type Catalog struct {
	endpoint string
	retries  int
	delayMs  int
}
type Option func(*Catalog)

// New applies options in declaration order; omitted options keep Go defaults.
//
//gobridge:constructor
func New(options ...Option) *Catalog {
	c := &Catalog{endpoint: "https://example.test", retries: 3}
	for _, option := range options {
		option(c)
	}
	return c
}

//gobridge:option endpoint
//gobridge:python base_url
//gobridge:ts baseURL
func WithEndpoint(endpoint string) Option { return func(c *Catalog) { c.endpoint = endpoint } }

//gobridge:option retries
func WithRetries(retries int) (Option, error) {
	if retries < 0 {
		return nil, gobridge.Failure("invalid_argument", "retries must be non-negative")
	}
	return func(c *Catalog) { c.retries = retries }, nil
}

//gobridge:python CatalogStatus
//gobridge:ts CatalogInfo
type Status struct {
	Endpoint string `json:"endpoint" python:"base_url" ts:"baseURL"`
	Retries  int    `json:"retries"`
}

//gobridge:export status
//gobridge:python get_status
//gobridge:ts getStatus
func (c *Catalog) Status() Status { return Status{c.endpoint, c.retries} }

//gobridge:export echo
func (c *Catalog) Echo(status Status) Status { return status }

// WithRetry configures a related pair of values atomically.
//
//gobridge:option retry
func WithRetry(attempts int, delayMs int) (Option, error) {
	if attempts < 0 || delayMs < 0 {
		return nil, gobridge.Failure("invalid_argument", "retry values must be non-negative")
	}
	return func(c *Catalog) { c.retries = attempts; c.delayMs = delayMs }, nil
}

//gobridge:export retry_delay
func (c *Catalog) RetryDelay() int { return c.delayMs }
