package phone

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mobai-app/simslim"
)

type Params struct {
	Conditions    []Condition       `json:"conditions,omitempty"`
	Mode          *string           `json:"mode,omitempty"`
	AfterEpoch    string            `json:"afterEpoch,omitempty"`
	AfterSequence uint64            `json:"afterSequence,omitempty"`
	TimeoutMs     *int              `json:"timeoutMs,omitempty"`
	Device        string            `json:"device,omitempty"`
	Name          string            `json:"name,omitempty"`
	DeviceType    string            `json:"deviceType,omitempty"`
	Runtime       string            `json:"runtime,omitempty"`
	Owner         string            `json:"owner,omitempty"`
	Lease         string            `json:"lease,omitempty"`
	Observation   string            `json:"observation,omitempty"`
	Ref           string            `json:"ref,omitempty"`
	Action        string            `json:"action,omitempty"`
	Text          string            `json:"text,omitempty"`
	Screenshot    bool              `json:"screenshot,omitempty"`
	App           string            `json:"app,omitempty"`
	BundleID      string            `json:"bundleId,omitempty"`
	Arguments     []string          `json:"arguments,omitempty"`
	Appearance    string            `json:"appearance,omitempty"`
	ContentSize   string            `json:"contentSize,omitempty"`
	Except        []string          `json:"except,omitempty"`
	Keep          []string          `json:"keep,omitempty"`
	Project       string            `json:"project,omitempty"`
	Scheme        string            `json:"scheme,omitempty"`
	Configuration string            `json:"configuration,omitempty"`
	DerivedData   string            `json:"derivedData,omitempty"`
	BuildSettings map[string]string `json:"buildSettings,omitempty"`
	Matrix        string            `json:"matrix,omitempty"`
}
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string     { return e.Message }
func fail(code, message string) error { return &APIError{code, message} }

type Request struct {
	Method string `json:"method"`
	Params Params `json:"params"`
}
type Response struct {
	OK     bool      `json:"ok"`
	Result any       `json:"result,omitempty"`
	Error  *APIError `json:"error,omitempty"`
}

func Result(value any, err error) Response {
	if err == nil {
		return Response{OK: true, Result: value}
	}
	var api *APIError
	if errors.Is(err, context.Canceled) {
		api = &APIError{"cancelled", "Operation cancelled"}
	} else if !errors.As(err, &api) {
		api = &APIError{"operation_failed", err.Error()}
	}
	return Response{Result: value, Error: api}
}

type Lease struct {
	Token   string    `json:"token"`
	Owner   string    `json:"owner"`
	Expires time.Time `json:"expires"`
}
type Phone struct {
	UDID            string `json:"udid"`
	Name            string `json:"name"`
	mu              phoneGate
	lease           *Lease
	bridge          phoneBridge
	observation     *Observation
	lastObservation *Observation
}

func (p *Phone) closeBridge() {
	if p.bridge != nil {
		p.bridge.Close()
		p.bridge = nil
	}
	p.observation = nil
}

type Service struct {
	vision       *FrameHub
	closeOnce    sync.Once
	root, data   string
	mu           sync.Mutex
	phones       map[string]*Phone
	bootMu       sync.Mutex
	matrixMu     sync.Mutex
	runCommand   func(context.Context, string, ...string) ([]byte, error)
	buildCommand func(*exec.Cmd) error
	eventMu      sync.Mutex
	memoryMu     sync.Mutex
	memoryAt     time.Time
	memory       map[string]simslim.Measurement
	memoryErrors map[string]string
	physical     int64
	budget       int64
	done         chan struct{}
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func mkdir(path string) error { return os.MkdirAll(path, 0700) }
func run(ctx context.Context, program string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, program, args...).CombinedOutput()
	if err != nil {
		if len(out) > 5000 {
			out = out[len(out)-5000:]
		}
		return nil, fmt.Errorf("%s: %w: %s", filepath.Base(program), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}
func (s *Service) command(ctx context.Context, program string, args ...string) ([]byte, error) {
	if s.runCommand != nil {
		return s.runCommand(ctx, program, args...)
	}
	return run(ctx, program, args...)
}
func atomicJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + "." + randomID() + ".tmp"
	if err = os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Rename(tmp, path)
}
func NewService(root, data string) (*Service, error) {
	if err := mkdir(data); err != nil {
		return nil, err
	}
	out, err := run(context.Background(), "/usr/sbin/sysctl", "-n", "hw.memsize")
	if err != nil {
		return nil, err
	}
	memory, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return nil, err
	}
	s := &Service{root: root, data: data, phones: map[string]*Phone{}, physical: memory, budget: memory / 2, done: make(chan struct{})}
	if value := os.Getenv("AGENT_PHONE_BUDGET_MIB"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 512 || n > memory/(1024*1024) {
			return nil, fmt.Errorf("invalid AGENT_PHONE_BUDGET_MIB")
		}
		s.budget = n * 1024 * 1024
	}
	raw, err := os.ReadFile(filepath.Join(data, "phones.json"))
	if err == nil {
		var saved []struct {
			UDID string `json:"udid"`
			Name string `json:"name"`
		}
		if err = json.Unmarshal(raw, &saved); err != nil {
			return nil, err
		}
		for _, p := range saved {
			s.phones[p.UDID] = &Phone{UDID: p.UDID, Name: p.Name}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	s.vision = newFrameHub(nativeFrameSource(root))
	go s.reapExpired()
	return s, nil
}
func (s *Service) reapExpired() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case now := <-ticker.C:
			s.mu.Lock()
			phones := make([]*Phone, 0, len(s.phones))
			for _, p := range s.phones {
				phones = append(phones, p)
			}
			s.mu.Unlock()
			for _, p := range phones {
				if p.mu.TryLock() {
					if p.lease != nil && !now.Before(p.lease.Expires) {
						p.closeBridge()
						p.lease = nil
						s.event(p.UDID, "lease_expired", nil)
					}
					p.mu.Unlock()
				}
			}
		}
	}
}
func (s *Service) Close() {
	s.closeOnce.Do(func() {
		if s.done != nil {
			close(s.done)
		}
		if s.vision != nil {
			s.vision.Close()
		}
		s.mu.Lock()
		phones := make([]*Phone, 0, len(s.phones))
		for _, p := range s.phones {
			phones = append(phones, p)
		}
		s.mu.Unlock()
		for _, p := range phones {
			p.mu.Lock()
			p.closeBridge()
			p.mu.Unlock()
		}
	})
}
func (s *Service) event(device, kind string, detail any) {
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	file, err := os.OpenFile(filepath.Join(s.data, "events.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	_ = json.NewEncoder(file).Encode(map[string]any{"time": time.Now(), "device": device, "kind": kind, "detail": detail})
}
func (s *Service) Call(ctx context.Context, r Request) (any, error) {
	return s.call(ctx, r, nil)
}

func (s *Service) call(ctx context.Context, r Request, goal *waitQuery) (value any, callErr error) {
	started := time.Now()
	a := r.Params
	var query waitQuery
	var diagnostics *WaitResult
	if r.Method == "wait" {
		var err error
		if goal != nil {
			query = *goal
		} else {
			query, err = parseWait(a)
		}
		if err != nil {
			return nil, err
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, started.Add(query.timeout))
		defer cancel()
		diagnostics = &WaitResult{Satisfied: []int{}}
		defer func() {
			waitDiagnostics(diagnostics, started)
			if value == nil {
				value = diagnostics
			}
			if callErr != nil {
				if cutoff := contextError(ctx); cutoff != nil {
					callErr = waitError(cutoff)
				}
			}
		}()
	}
	switch r.Method {
	case "fleet":
		return s.fleet(ctx)
	case "catalog":
		out, err := run(ctx, "/usr/bin/xcrun", "simctl", "list", "--json")
		if err != nil {
			return nil, err
		}
		return json.RawMessage(out), nil
	case "create":
		return s.create(ctx, a)
	case "build":
		return s.build(ctx, a)
	case "matrix":
		return s.matrix(ctx, a.Matrix)
	}
	s.mu.Lock()
	p := s.phones[a.Device]
	s.mu.Unlock()
	if p == nil {
		return nil, fail("unmanaged_device", "Select a device created by agent-phone. Existing simulators are read-only.")
	}
	if r.Method == "frame" || r.Method == "wait_frame" {
		return s.frame(ctx, r.Method, a)
	}
	if err := p.mu.LockContext(ctx); err != nil {
		return nil, err
	}
	defer p.mu.Unlock()
	if p.lease != nil && !time.Now().Before(p.lease.Expires) {
		p.lease = nil
		p.closeBridge()
	}
	if r.Method == "acquire" {
		if a.Owner == "" || len(a.Owner) > 120 {
			return nil, fmt.Errorf("owner is required (up to 120 characters)")
		}
		if p.lease != nil {
			return nil, fail("device_busy", fmt.Sprintf("Device is controlled by %s until %s", p.lease.Owner, p.lease.Expires.Format(time.RFC3339)))
		}
		p.lease = &Lease{randomID(), a.Owner, time.Now().Add(30 * time.Minute)}
		s.event(p.UDID, "acquire", map[string]any{"owner": a.Owner})
		copy := *p.lease
		return &copy, nil
	}
	if err := validateLease(p.lease, a.Lease, time.Now()); err != nil {
		return nil, err
	}
	p.lease.Expires = time.Now().Add(30 * time.Minute)
	switch r.Method {
	case "release":
		p.closeBridge()
		p.lease = nil
		s.event(p.UDID, "release", nil)
		return map[string]any{"released": true}, nil
	case "boot":
		return s.boot(ctx, p, a)
	case "shutdown":
		if s.vision != nil {
			resume := s.vision.pause(p.UDID)
			defer resume()
		}
		p.closeBridge()
		err := simslim.Shutdown(ctx, "default", p.UDID)
		s.invalidateMemory()
		return map[string]any{"shutdown": err == nil}, err
	case "wait":
		return s.wait(ctx, p, a, query, started, diagnostics)
	case "observe":
		return s.observe(ctx, p, a.Screenshot)
	case "act":
		return s.act(ctx, p, a)
	case "install":
		if !filepath.IsAbs(a.App) || filepath.Ext(a.App) != ".app" {
			return nil, fmt.Errorf("app must be an absolute .app bundle path")
		}
		p.closeBridge()
		_, err := run(ctx, "/usr/bin/xcrun", "simctl", "install", p.UDID, a.App)
		return map[string]any{"installed": err == nil, "app": a.App}, err
	case "launch":
		if a.BundleID == "" || strings.HasPrefix(a.BundleID, "-") {
			return nil, fmt.Errorf("bundleId is required")
		}
		p.closeBridge()
		args := append([]string{"simctl", "launch", "--terminate-running-process", p.UDID, a.BundleID}, a.Arguments...)
		out, err := run(ctx, "/usr/bin/xcrun", args...)
		return map[string]any{"launch": strings.TrimSpace(string(out))}, err
	case "configure":
		if a.Appearance != "" {
			if a.Appearance != "light" && a.Appearance != "dark" {
				return nil, fmt.Errorf("appearance must be light or dark")
			}
			if _, err := run(ctx, "/usr/bin/xcrun", "simctl", "ui", p.UDID, "appearance", a.Appearance); err != nil {
				return nil, err
			}
		}
		if a.ContentSize != "" {
			valid := map[string]bool{"extra-small": true, "small": true, "medium": true, "large": true, "extra-large": true, "extra-extra-large": true, "extra-extra-extra-large": true, "accessibility-medium": true, "accessibility-large": true, "accessibility-extra-large": true, "accessibility-extra-extra-large": true, "accessibility-extra-extra-extra-large": true}
			if !valid[a.ContentSize] {
				return nil, fmt.Errorf("unsupported contentSize")
			}
			if _, err := run(ctx, "/usr/bin/xcrun", "simctl", "ui", p.UDID, "content_size", a.ContentSize); err != nil {
				return nil, err
			}
		}
		p.observation = nil
		return map[string]any{"configured": true}, nil
	default:
		return nil, fail("unknown_method", "Unknown method: "+r.Method)
	}
}
func validateLease(lease *Lease, token string, now time.Time) error {
	if lease == nil || token == "" || lease.Token != token || !now.Before(lease.Expires) {
		return fail("lease_required", "Acquire this device and supply its current lease token.")
	}
	return nil
}
func (s *Service) create(ctx context.Context, a Params) (any, error) {
	if strings.TrimSpace(a.Name) == "" || len(a.Name) > 80 || a.DeviceType == "" || a.Runtime == "" {
		return nil, fmt.Errorf("name, deviceType and runtime are required (use catalog)")
	}
	out, err := s.command(ctx, "/usr/bin/xcrun", "simctl", "create", "agent-phone · "+a.Name, a.DeviceType, a.Runtime)
	if err != nil {
		return nil, err
	}
	p := &Phone{UDID: strings.TrimSpace(string(out)), Name: a.Name}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.phones[p.UDID] = p
	saved := make([]map[string]string, 0, len(s.phones))
	for _, phone := range s.phones {
		saved = append(saved, map[string]string{"udid": phone.UDID, "name": phone.Name})
	}
	if err = atomicJSON(filepath.Join(s.data, "phones.json"), saved); err != nil {
		return nil, fmt.Errorf("created %s but could not persist registry: %w", p.UDID, err)
	}
	s.event(p.UDID, "create", map[string]any{"name": a.Name})
	return p, nil
}
func (s *Service) invalidateMemory() {
	s.memoryMu.Lock()
	s.memoryAt = time.Time{}
	s.memoryMu.Unlock()
}
func (s *Service) measure(ctx context.Context, devices []simslim.Device, fresh bool) (map[string]simslim.Measurement, map[string]string) {
	s.memoryMu.Lock()
	defer s.memoryMu.Unlock()
	if fresh || time.Since(s.memoryAt) > 5*time.Second {
		var ids []string
		for _, d := range devices {
			if d.State == "Booted" {
				ids = append(ids, d.UDID)
			}
		}
		s.memory, s.memoryErrors = simslim.MeasureMany(ctx, ids)
		s.memoryAt = time.Now()
	}
	return s.memory, s.memoryErrors
}
func budgetFits(used, estimate, budget int64) bool {
	return used >= 0 && estimate > 0 && budget > 0 && used <= budget-estimate
}
func (s *Service) boot(ctx context.Context, p *Phone, a Params) (any, error) {
	s.bootMu.Lock()
	defer s.bootMu.Unlock()
	devices, err := simslim.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	already := false
	for _, d := range devices {
		if d.UDID == p.UDID && d.State == "Booted" {
			already = true
		}
	}
	if !already {
		measurements, unknown := s.measure(ctx, devices, true)
		if len(unknown) > 0 {
			return nil, fail("memory_unknown", "Could not measure all running simulators; retry before booting another.")
		}
		var used int64
		for _, m := range measurements {
			used += m.Bytes
		}
		const estimate = int64(1200 * 1024 * 1024)
		if !budgetFits(used, estimate, s.budget) {
			return nil, fail("capacity", fmt.Sprintf("Fleet uses %.0f MiB; another phone reserves 1200 MiB against a %.0f MiB budget. Shut down an idle managed phone or queue the variation.", float64(used)/(1024*1024), float64(s.budget)/(1024*1024)))
		}
		if pressure, err := run(ctx, "/usr/sbin/sysctl", "-n", "kern.memorystatus_vm_pressure_level"); err == nil {
			n, _ := strconv.Atoi(strings.TrimSpace(string(pressure)))
			if n >= 4 {
				return nil, fail("memory_pressure", "macOS reports critical memory pressure; defer this boot.")
			}
		}
	}
	// Apps may register for push during startup even in a simulator. Removing
	// apsd makes some apps abort before their first screen appears.
	keep := append([]string{"com.apple.apsd"}, a.Keep...)
	profile, err := simslim.BuildProfile("", strings.Join(a.Except, ","), strings.Join(keep, ","))
	if err != nil {
		return nil, err
	}
	if s.vision != nil {
		resume := s.vision.pause(p.UDID)
		defer resume()
	}
	p.closeBridge()
	changed, err := simslim.EnableSlim(ctx, "default", p.UDID, profile, func(message string) { s.event(p.UDID, "boot_progress", message) })
	s.invalidateMemory()
	return map[string]any{"booted": err == nil, "profileChanged": changed}, err
}
func (s *Service) fleet(ctx context.Context) (any, error) {
	devices, err := simslim.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	measured, unknown := s.measure(ctx, devices, false)
	s.mu.Lock()
	phones := make(map[string]*Phone, len(s.phones))
	for id, p := range s.phones {
		phones[id] = p
	}
	s.mu.Unlock()
	rows := []map[string]any{}
	var used int64
	for _, d := range devices {
		row := map[string]any{"udid": d.UDID, "name": d.Name, "state": d.State, "runtime": d.RuntimeName(), "managed": false}
		if m, ok := measured[d.UDID]; ok && d.State == "Booted" {
			row["memoryBytes"] = m.Bytes
			row["processes"] = m.Processes
			used += m.Bytes
		}
		if e := unknown[d.UDID]; e != "" {
			row["measurementError"] = e
		}
		if p := phones[d.UDID]; p != nil {
			row["managed"] = true
			row["name"] = p.Name
			if p.mu.TryLock() {
				if p.lease != nil && time.Now().Before(p.lease.Expires) {
					row["owner"] = p.lease.Owner
					row["leaseExpires"] = p.lease.Expires
				}
				if p.lastObservation != nil {
					row["observation"] = p.lastObservation
				}
				p.mu.Unlock()
			} else {
				row["busy"] = true
			}
		}
		rows = append(rows, row)
	}
	return map[string]any{"phones": rows, "physicalMemoryBytes": s.physical, "budgetBytes": s.budget, "simulatorMemoryBytes": used, "measuredAt": time.Now(), "dataDirectory": s.data}, nil
}
