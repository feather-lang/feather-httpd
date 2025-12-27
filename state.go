package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/feather-lang/feather"
)

type Route struct {
	Method  string
	Pattern string
	Params  []string // parameter names extracted from pattern
	Body    string   // TCL script to execute
}

type RequestContext struct {
	mu      sync.Mutex
	Writer  http.ResponseWriter
	Request *http.Request
	Params  map[string]string
	Status  int
	Headers sync.Map // string -> string
	Written bool
}

// Connection represents a held HTTP connection for streaming
type Connection struct {
	ID        string
	Name      string // optional user-provided name
	Ctx       *RequestContext
	Opened    time.Time
	Done      chan struct{} // closed when connection should end
	OnClose   string        // Feather proc to call when connection closes
}

type EvalContext struct {
	Output func(string) // callback for puts output
}

// EvalRequest represents a request to evaluate code on the interpreter
type EvalRequest struct {
	Script   string
	Response chan EvalResponse
}

// EvalResponse contains the result of an eval request
type EvalResponse struct {
	Result feather.Object
	Error  error
}

type ServerState struct {
	mu              sync.RWMutex
	routes          []Route
	server          *http.Server
	shutdown        chan struct{}
	reqCtx          *RequestContext    // current request context (per-request)
	evalCtx         *EvalContext       // current eval context (for web REPL)
	templates       *template.Template
	templateSources sync.Map           // string -> string, raw template content
	connections     sync.Map           // string -> *Connection, by ID or name
	evalChan        chan EvalRequest   // channel for serializing interpreter access
}

func NewServerState() *ServerState {
	return &ServerState{
		routes:    make([]Route, 0),
		shutdown:  make(chan struct{}),
		templates: template.New(""),
		evalChan:  make(chan EvalRequest),
	}
}

// RunInterpreter runs the interpreter loop, processing eval requests sequentially.
// This must be called from the main goroutine after registering commands.
func (s *ServerState) RunInterpreter(interp *feather.Interp) {
	for {
		select {
		case <-s.shutdown:
			return
		case req := <-s.evalChan:
			result, err := interp.Eval(req.Script)
			req.Response <- EvalResponse{Result: result, Error: err}
		}
	}
}

// Eval sends a script to the interpreter and waits for the result.
// This is safe to call from any goroutine.
func (s *ServerState) Eval(script string) (feather.Object, error) {
	resp := make(chan EvalResponse, 1)
	s.evalChan <- EvalRequest{Script: script, Response: resp}
	r := <-resp
	return r.Result, r.Error
}

func (s *ServerState) LoadTemplate(name, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.templateSources.Store(name, content)
	_, err := s.templates.New(name).Parse(content)
	return err
}

// ReparseTemplates rebuilds all templates from sources
func (s *ServerState) ReparseTemplates() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create fresh template set
	newTemplates := template.New("")

	// Reparse all sources
	var parseErr error
	s.templateSources.Range(func(key, value any) bool {
		name := key.(string)
		content := value.(string)
		_, err := newTemplates.New(name).Parse(content)
		if err != nil {
			parseErr = fmt.Errorf("template %s: %v", name, err)
			return false
		}
		return true
	})

	if parseErr != nil {
		return parseErr
	}

	s.templates = newTemplates
	return nil
}

func (s *ServerState) GetTemplate(name string) *template.Template {
	srcVal, ok := s.templateSources.Load(name)
	if !ok {
		return nil
	}
	src := srcVal.(string)

	// Clone base templates while holding lock to prevent concurrent modification
	s.mu.Lock()
	clone, err := s.templates.Clone()
	s.mu.Unlock()
	if err != nil {
		return nil
	}

	// Parse outside of lock - clone is our own copy
	tmpl, err := clone.New(name).Parse(src)
	if err != nil {
		return nil
	}
	return tmpl
}

func (s *ServerState) ListTemplates() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var names []string
	for _, t := range s.templates.Templates() {
		if t.Name() != "" {
			names = append(names, t.Name())
		}
	}
	return names
}

func (s *ServerState) GetTemplateSource(name string) string {
	if val, ok := s.templateSources.Load(name); ok {
		return val.(string)
	}
	return ""
}

func (s *ServerState) AddRoute(method, pattern, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	params := extractParams(pattern)
	newRoute := Route{
		Method:  method,
		Pattern: pattern,
		Params:  params,
		Body:    body,
	}

	// Check for existing route with same method and pattern
	for i, r := range s.routes {
		if r.Method == method && r.Pattern == pattern {
			s.routes[i] = newRoute
			return
		}
	}

	s.routes = append(s.routes, newRoute)
}

func (s *ServerState) GetRoutes() []Route {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Route{}, s.routes...)
}

func (s *ServerState) SetRequestContext(ctx *RequestContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reqCtx = ctx
}

func (s *ServerState) GetRequestContext() *RequestContext {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.reqCtx
}

func (s *ServerState) SetEvalContext(ctx *EvalContext) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evalCtx = ctx
}

func (s *ServerState) GetEvalContext() *EvalContext {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.evalCtx
}

// HoldConnection creates a held connection from the current request context
func (s *ServerState) HoldConnection(name string) (*Connection, error) {
	s.mu.Lock()
	reqCtx := s.reqCtx
	s.mu.Unlock()

	if reqCtx == nil {
		return nil, fmt.Errorf("not in request context")
	}

	// Generate unique ID
	id := generateID()

	conn := &Connection{
		ID:     id,
		Name:   name,
		Ctx:    reqCtx,
		Opened: time.Now(),
		Done:   make(chan struct{}),
	}

	// Store by ID
	s.connections.Store(id, conn)

	// Also store by name if provided
	if name != "" {
		s.connections.Store(name, conn)
	}

	return conn, nil
}

// GetConnection retrieves a connection by ID or name
func (s *ServerState) GetConnection(handle string) *Connection {
	if val, ok := s.connections.Load(handle); ok {
		return val.(*Connection)
	}
	return nil
}

// CloseConnection closes and removes a connection
func (s *ServerState) CloseConnection(handle string) error {
	val, ok := s.connections.Load(handle)
	if !ok {
		return fmt.Errorf("unknown connection: %s", handle)
	}
	conn := val.(*Connection)

	// Close the done channel to signal the handler
	select {
	case <-conn.Done:
		// Already closed
	default:
		close(conn.Done)
	}

	// Remove from map (both ID and name)
	s.connections.Delete(conn.ID)
	if conn.Name != "" {
		s.connections.Delete(conn.Name)
	}

	return nil
}

// ListConnections returns all connection handles
func (s *ServerState) ListConnections() []string {
	seen := make(map[string]bool)
	var handles []string
	s.connections.Range(func(key, value any) bool {
		conn := value.(*Connection)
		if !seen[conn.ID] {
			seen[conn.ID] = true
			if conn.Name != "" {
				handles = append(handles, conn.Name)
			} else {
				handles = append(handles, conn.ID)
			}
		}
		return true
	})
	return handles
}

// findConnectionByContext finds a connection that uses the given request context
func (s *ServerState) findConnectionByContext(ctx *RequestContext) *Connection {
	var found *Connection
	s.connections.Range(func(key, value any) bool {
		conn := value.(*Connection)
		if conn.Ctx == ctx {
			found = conn
			return false // stop iteration
		}
		return true
	})
	return found
}

func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return "conn-" + hex.EncodeToString(b)
}

func extractParams(pattern string) []string {
	var params []string
	parts := splitPath(pattern)
	for _, part := range parts {
		if len(part) > 0 && part[0] == ':' {
			params = append(params, part[1:])
		}
	}
	return params
}

func splitPath(path string) []string {
	var parts []string
	current := ""
	for _, c := range path {
		if c == '/' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

func matchRoute(route Route, method, path string) (bool, map[string]string) {
	if route.Method != method {
		return false, nil
	}

	patternParts := splitPath(route.Pattern)
	pathParts := splitPath(path)

	if len(patternParts) != len(pathParts) {
		return false, nil
	}

	params := make(map[string]string)
	for i, pp := range patternParts {
		if len(pp) > 0 && pp[0] == ':' {
			params[pp[1:]] = pathParts[i]
		} else if pp != pathParts[i] {
			return false, nil
		}
	}

	return true, params
}
