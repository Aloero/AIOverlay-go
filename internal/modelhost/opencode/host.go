// Package opencode runs the OpenCode runtime that ships in the release archive as a local model
// host: one child process with its own private storage, its own control password and the model
// catalog the application validated.
//
// Everything that is true only of *this* host lives here: the `serve` command line, the isolated
// HOME/XDG profile, the catalog pin, the health check. The protocol adapter in
// internal/providers talks to a modelhost.Endpoint and would work unchanged against any other
// host that speaks the same HTTP API, which is the point of the boundary.
package opencode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"aioverlay/internal/childprocess"
	"aioverlay/internal/modelhost"
)

// HostID names this implementation in endpoints, traces and the Studio capability table.
const HostID = "opencode"

// defaultControlUser is the basic-auth user the OpenCode server expects; it is a protocol detail
// of this host, not a secret.
const defaultControlUser = "opencode"

type Config struct {
	// Binary is the opencode executable. "" discovers it: AIOVERLAY_OPENCODE_BIN first, then the
	// directory next to the application executable, then the working directory.
	Binary string
	// CatalogPath is the models.dev snapshot to pin. "" discovers the bundled one.
	CatalogPath string
	// ProviderID selects which upstream provider inside OpenCode this host serves ("opencode").
	ProviderID string
	// UpstreamURL optionally overrides that provider's base URL.
	UpstreamURL string
	// CredentialEnv names the environment variable the selected key is injected as.
	CredentialEnv []string
	// Startup bounds one cold boot. Zero uses modelhost.StartupTimeout.
	Startup time.Duration
	// HealthInterval is the readiness poll step. Zero uses 80ms.
	HealthInterval time.Duration
}

// Host starts and reuses OpenCode child processes.
type Host struct {
	cfg  Config
	pool modelhost.Pool

	versionOnce sync.Once
	version     string
}

func New(cfg Config) *Host {
	if cfg.ProviderID == "" {
		cfg.ProviderID = "opencode"
	}
	if cfg.Startup <= 0 {
		cfg.Startup = modelhost.StartupTimeout
	}
	if cfg.HealthInterval <= 0 {
		cfg.HealthInterval = 80 * time.Millisecond
	}
	return &Host{cfg: cfg}
}

func (h *Host) ID() string { return HostID }

// Version reads the release archive's VERSION.txt next to the runtime binary, so a trace can name
// the exact build that answered. "" means unknown, never a guess.
func (h *Host) Version() string {
	h.versionOnce.Do(func() { h.version = h.readVersion() })
	return h.version
}

func (h *Host) readVersion() string {
	bin, err := h.binary()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(bin), "VERSION.txt"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if line == "" {
			continue
		}
		if i := strings.LastIndexAny(line, " vV"); i >= 0 {
			candidate := strings.TrimLeft(strings.TrimSpace(line[i:]), "vV")
			if strings.ContainsRune(candidate, '.') {
				return candidate
			}
		}
		return line
	}
	return ""
}

// Endpoint returns a usable control endpoint for the requested configuration plus its lease.
func (h *Host) Endpoint(ctx context.Context, need modelhost.Need) (modelhost.Endpoint, func(), error) {
	lease, release, err := h.pool.Acquire(ctx, h.key(need), func(startCtx context.Context) (modelhost.Instance, error) {
		return h.boot(startCtx, need)
	})
	if err != nil {
		return modelhost.Endpoint{}, func() {}, err
	}
	server, ok := lease.(*instance)
	if !ok {
		release()
		return modelhost.Endpoint{}, func() {}, fmt.Errorf("opencode host pool holds an unexpected instance type")
	}
	return server.endpoint(h.Version()), release, nil
}

// Served is not implemented yet, and saying so is the reason this method exists: the host, not the
// application, has to be the one to say which models it accepts. Until this returns real data the
// app's catalog stays an assumption. The route to verify against a live runtime on Windows is
// `GET /config/providers` (present in the 1.18.31 binary next to /global/health); its response
// shape has not been confirmed, and guessing it would report "no models" instead of "unknown".
func (h *Host) Served(context.Context, modelhost.Need) ([]modelhost.ModelRef, error) {
	return nil, modelhost.ErrServedUnsupported
}

func (h *Host) Close() { h.pool.Close() }

// key composes the pool identity. These are exactly the values the child is configured with, so two
// requests that need different ones must never share a process.
func (h *Host) key(need modelhost.Need) string {
	return strings.Join([]string{
		need.Credential,
		need.Proxy,
		catalogGeneration(h.catalogPath(need)),
		strconv.Itoa(need.OutputTokenCap),
	}, "\x00")
}

// catalogGeneration identifies which snapshot version the child boots with. The application
// refreshes that file in place, so a warm runtime would otherwise keep a model list the app has
// already replaced - the same divergence the pin exists to remove, arriving through the warm path.
func catalogGeneration(path string) string {
	if path == "" {
		return "unpinned"
	}
	info, err := os.Stat(path)
	if err != nil {
		return "unpinned"
	}
	return strconv.FormatInt(info.Size(), 10) + "-" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

func (h *Host) binary() (string, error) {
	if override := strings.TrimSpace(h.cfg.Binary); override != "" {
		if st, err := os.Stat(override); err == nil && !st.IsDir() {
			return filepath.Abs(override)
		}
	}
	if fromEnv := strings.TrimSpace(os.Getenv("AIOVERLAY_OPENCODE_BIN")); fromEnv != "" {
		if st, err := os.Stat(fromEnv); err == nil && !st.IsDir() {
			return filepath.Abs(fromEnv)
		}
	}
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "runtime", "opencode", "opencode.exe"), filepath.Join(dir, "runtime", "opencode", "opencode"))
	}
	candidates = append(candidates, filepath.Join("runtime", "opencode", "opencode.exe"), filepath.Join("runtime", "opencode", "opencode"))
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return filepath.Abs(p)
		}
	}
	return "", fmt.Errorf("embedded OpenCode runtime not found")
}

// BundledCatalog is the models.dev snapshot the release archive ships next to the executable - the
// same file internal/app opens through resourcePath("runtime","catalog",...). The executable's
// directory wins because the overlay also starts from Task Scheduler, whose working directory is
// the user profile.
func BundledCatalog() string {
	candidates := []string{}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), "runtime", "catalog", "models-dev-api.json"))
	}
	candidates = append(candidates, filepath.Join("runtime", "catalog", "models-dev-api.json"))
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func (h *Host) catalogPath(need modelhost.Need) string {
	if path := strings.TrimSpace(need.Catalog); path != "" {
		return path
	}
	if path := strings.TrimSpace(h.cfg.CatalogPath); path != "" {
		return path
	}
	return BundledCatalog()
}

func (h *Host) boot(ctx context.Context, need modelhost.Need) (modelhost.Instance, error) {
	server := &instance{
		host: h, credential: need.Credential, upstreamProxy: need.Proxy,
		catalogPath: h.catalogPath(need), outputTokenCap: need.OutputTokenCap,
	}
	if err := server.start(ctx); err != nil {
		server.Stop()
		return nil, err
	}
	return server, nil
}

// instance is one `opencode serve` child process with a private profile directory.
type instance struct {
	host           *Host
	credential     string
	upstreamProxy  string
	catalogPath    string
	outputTokenCap int

	port      int
	password  string
	cmd       *exec.Cmd
	process   *childprocess.Process
	tempDir   string
	startedOK bool
}

func (i *instance) endpoint(version string) modelhost.Endpoint {
	return modelhost.Endpoint{
		BaseURL:  "http://127.0.0.1:" + strconv.Itoa(i.port),
		Username: defaultControlUser,
		Password: i.password,
		Host:     HostID,
		Version:  version,
	}
}

func (i *instance) Alive() bool { return i.process != nil && i.process.Alive() }

func (i *instance) Stop() {
	if i.process != nil {
		i.process.Stop()
		i.process = nil
	}
	if i.tempDir != "" {
		if err := os.RemoveAll(i.tempDir); err != nil {
			log.Printf("OpenCode temporary profile cleanup failed: %v", err)
		}
		i.tempDir = ""
	}
}

func (i *instance) start(ctx context.Context) error {
	bin, err := i.host.binary()
	if err != nil {
		return err
	}
	// Reserve the port, then release it: the child binds it. The window is unavoidable without
	// passing a socket, and the child fails fast (and retried by the pool) if something took it.
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	i.port = ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return fmt.Errorf("OS random source unavailable for OpenCode control credentials")
	}
	i.password = hex.EncodeToString(token)
	i.tempDir, err = os.MkdirTemp("", "aioverlay-go-opencode-")
	if err != nil {
		return err
	}

	options := map[string]any{}
	if i.credential != "" {
		options["apiKey"] = i.credential
	}
	if i.host.cfg.UpstreamURL != "" {
		options["baseURL"] = i.host.cfg.UpstreamURL
	}
	providerID := i.host.cfg.ProviderID
	cfg := map[string]any{
		"provider":          map[string]any{providerID: map[string]any{"options": options}},
		"enabled_providers": []string{providerID},
		// The runtime is used as a model gateway only: it may not touch files or run tools.
		"tools":      map[string]bool{"*": false},
		"permission": map[string]string{"*": "deny"},
	}
	cfgJSON, _ := json.Marshal(cfg)

	i.cmd = exec.Command(bin, "serve", "--pure", "--hostname", "127.0.0.1", "--port", strconv.Itoa(i.port))
	i.cmd.Env = i.environment(cfgJSON)
	i.cmd.Dir = i.tempDir
	// The runtime writes its own log inside the profile directory (data/opencode/log), which is the
	// only place upstream failures are explained in words; stdout/stderr would only duplicate it
	// into a pipe nobody drains.
	i.cmd.Stdout = io.Discard
	i.cmd.Stderr = io.Discard
	// childprocess.Start applies the hidden-window creation flags and puts the child in a
	// kill-on-close job object, so no per-caller SysProcAttr is set here.
	i.process, err = childprocess.Start(i.cmd)
	if err != nil {
		return err
	}
	i.startedOK = true
	return i.waitReady(ctx)
}

// waitReady polls the host's own health route. Failure is a plain typed error, not an outcome:
// deciding what a failed boot means for the request belongs to the adapter.
func (i *instance) waitReady(ctx context.Context) error {
	for {
		var health struct {
			Healthy bool `json:"healthy"`
		}
		status, _, err := i.endpoint("").DoJSON(ctx, http.MethodGet, "/global/health", nil, &health, time.Second)
		if err == nil && status/100 == 2 && health.Healthy {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %s did not become healthy within %s (%v)", modelhost.ErrStartupTimeout, HostID, i.host.cfg.Startup, ctx.Err())
		case <-time.After(i.host.cfg.HealthInterval):
		}
	}
}

// environment is the whole child environment, built from scratch: only ordinary process-launch and
// TLS variables are inherited. In particular no OpenCode config, credential file or proxy bypass of
// the surrounding user leaks into the runtime.
func (i *instance) environment(cfgJSON []byte) []string {
	env := childprocess.SanitizedEnv()
	if i.outputTokenCap > 0 {
		env = append(env, "OPENCODE_EXPERIMENTAL_OUTPUT_TOKEN_MAX="+strconv.Itoa(i.outputTokenCap))
	}
	env = append(env,
		"HOME="+i.tempDir, "USERPROFILE="+i.tempDir,
		"APPDATA="+filepath.Join(i.tempDir, "config"), "LOCALAPPDATA="+filepath.Join(i.tempDir, "data"),
		"XDG_CONFIG_HOME="+filepath.Join(i.tempDir, "config"), "XDG_DATA_HOME="+filepath.Join(i.tempDir, "data"),
		"XDG_CACHE_HOME="+filepath.Join(i.tempDir, "cache"), "XDG_STATE_HOME="+filepath.Join(i.tempDir, "state"),
		"OPENCODE_SERVER_PASSWORD="+i.password, "OPENCODE_CONFIG_CONTENT="+string(cfgJSON),
		"HTTPS_PROXY="+i.upstreamProxy, "HTTP_PROXY="+i.upstreamProxy, "NO_PROXY=127.0.0.1,localhost",
	)
	if i.credential != "" && len(i.host.cfg.CredentialEnv) > 0 {
		env = append(env, i.host.cfg.CredentialEnv[0]+"="+i.credential)
	}
	// The app decides which models exist from its own models.dev snapshot and refreshes that same
	// file. Without the pin the child downloads its own copy, so the two lists drift: measured on a
	// live guest, the runtime refused a model the app had marked ready with "Model not found:
	// opencode/minimax-m3.1-flash-free. Did you mean: ling-3.0-flash-fin-free, mimo-v2.5-free?" - it
	// was still proposing models upstream had retired. Pinning keeps host and app on one catalog and
	// skips a 6 MB download per cold start.
	//
	// The order inside the runtime is: OPENCODE_MODELS_PATH, then its cache, then the network - and
	// OPENCODE_DISABLE_MODELS_FETCH *without* a usable path leaves it with an empty catalog. So the
	// pair is applied only to a file that exists and is plausibly complete.
	if path := strings.TrimSpace(i.catalogPath); path != "" {
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Size() > 1<<20 {
			env = append(env, "OPENCODE_DISABLE_MODELS_FETCH=1", "OPENCODE_MODELS_PATH="+path)
		}
	}
	return env
}
