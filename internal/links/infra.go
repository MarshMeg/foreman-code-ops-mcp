package links

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/MarshMeg/foreman-code-ops-mcp/internal/project"
)

// infra is what docker-compose, .env files and contracts say about services.
type infra struct {
	// envTarget maps an environment variable holding a base URL to the service it points to.
	envTarget map[string]string
	// envPath is the path part of such a base URL ("/api"), prepended to call paths.
	envPath map[string]string
	// hostTarget maps hostnames (compose service names) to workspace services.
	hostTarget map[string]string
	// ops are contract operations per service: "METHOD path" (normalized) or "GRPC /pkg.Svc/Rpc".
	ops map[string]map[string]bool
	// grpc lists protobuf services: service name -> rpcs, owned by a workspace service.
	grpc []grpcService
}

type grpcService struct {
	owner, file, pkg, name string
	rpcs                   []string
}

var composeFiles = []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"}

type composeFile struct {
	Services map[string]struct {
		Build       any   `yaml:"build"`
		Environment any   `yaml:"environment"`
		EnvFile     any   `yaml:"env_file"`
		Ports       []any `yaml:"ports"`
	} `yaml:"services"`
}

func loadInfra(root string, svcs []project.Service) *infra {
	in := &infra{envTarget: map[string]string{}, envPath: map[string]string{}, hostTarget: map[string]string{}, ops: map[string]map[string]bool{}}
	byName := map[string]string{}
	byRoot := map[string]string{}
	for _, s := range svcs {
		byName[s.Name] = s.Name
		byRoot[path.Clean(s.Root)] = s.Name
		in.hostTarget[s.Name] = s.Name
	}
	env := map[string]string{} // VAR -> value, from every source

	for _, cf := range composeFiles {
		b, err := os.ReadFile(filepath.Join(root, cf))
		if err != nil {
			continue
		}
		var c composeFile
		if yaml.Unmarshal(b, &c) != nil {
			continue
		}
		for name, s := range c.Services {
			target := byName[name]
			ctx := ""
			switch bv := s.Build.(type) {
			case string:
				ctx = bv
			case map[string]any:
				ctx, _ = bv["context"].(string)
			}
			if ctx != "" {
				if t, ok := byRoot[path.Clean(strings.TrimPrefix(ctx, "./"))]; ok {
					target = t
				}
			}
			if target != "" {
				in.hostTarget[name] = target
			}
			for k, v := range envMap(s.Environment) {
				env[k] = v
			}
		}
	}
	dirs := []string{"."}
	for _, s := range svcs {
		dirs = append(dirs, s.Root)
	}
	for _, d := range dirs {
		for _, f := range []string{".env", ".env.local", ".env.development", ".env.example", ".env.sample"} {
			for k, v := range readDotEnv(filepath.Join(root, d, f)) {
				if _, set := env[k]; !set {
					env[k] = v
				}
			}
		}
	}
	for k, v := range env {
		u, err := url.Parse(strings.Trim(v, `"'`))
		if err != nil || u.Host == "" {
			continue
		}
		if t, ok := in.hostTarget[u.Hostname()]; ok {
			in.envTarget[k] = t
		}
		if p := strings.TrimRight(u.Path, "/"); p != "" {
			in.envPath[k] = p
		}
	}
	for _, s := range svcs {
		for _, c := range s.Consumes {
			if c.BaseURLEnv != "" && c.Service != "" {
				in.envTarget[c.BaseURLEnv] = c.Service
			}
		}
	}
	in.loadContracts(root, svcs)
	return in
}

func envMap(v any) map[string]string {
	out := map[string]string{}
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				if k, val, ok := strings.Cut(s, "="); ok {
					out[k] = val
				}
			}
		}
	}
	return out
}

func readDotEnv(p string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(p)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return out
}

var openAPINames = []string{"openapi.yaml", "openapi.yml", "openapi.json", "swagger.yaml", "swagger.yml", "swagger.json",
	"api/openapi.yaml", "api/openapi.yml", "api/openapi.json", "docs/openapi.yaml", "docs/swagger.yaml"}

var (
	protoPackage = regexp.MustCompile(`(?m)^\s*package\s+([\w.]+)\s*;`)
	protoService = regexp.MustCompile(`(?s)service\s+(\w+)\s*\{(.*?)\n\}`)
	protoRPC     = regexp.MustCompile(`rpc\s+(\w+)\s*\(`)
)

func (in *infra) loadContracts(root string, svcs []project.Service) {
	for _, s := range svcs {
		files := map[string]bool{}
		if p, ok := s.Exposes["openapi"].(string); ok {
			files[p] = true
		}
		for _, n := range openAPINames {
			files[path.Join(s.Root, n)] = true
		}
		for f := range files {
			b, err := os.ReadFile(filepath.Join(root, f))
			if err != nil {
				continue
			}
			for op := range parseOpenAPI(b) {
				if in.ops[s.Name] == nil {
					in.ops[s.Name] = map[string]bool{}
				}
				in.ops[s.Name][op] = true
			}
		}
		// The callback skips unreadable entries, so the walk itself never fails.
		_ = filepath.WalkDir(filepath.Join(root, s.Root), func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			if d.IsDir() || filepath.Ext(p) != ".proto" {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			pkg := ""
			if m := protoPackage.FindSubmatch(b); m != nil {
				pkg = string(m[1])
			}
			rel, _ := filepath.Rel(root, p)
			for _, sm := range protoService.FindAllSubmatch(b, -1) {
				g := grpcService{owner: s.Name, file: filepath.ToSlash(rel), pkg: pkg, name: string(sm[1])}
				for _, rm := range protoRPC.FindAllSubmatch(sm[2], -1) {
					g.rpcs = append(g.rpcs, string(rm[1]))
				}
				in.grpc = append(in.grpc, g)
			}
			return nil
		})
	}
}

// parseOpenAPI returns "METHOD /path" operations (paths normalized, basePath applied).
func parseOpenAPI(b []byte) map[string]bool {
	var doc struct {
		BasePath string                    `yaml:"basePath" json:"basePath"`
		Paths    map[string]map[string]any `yaml:"paths" json:"paths"`
	}
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		if json.Unmarshal(b, &doc) != nil {
			return nil
		}
	} else if yaml.Unmarshal(b, &doc) != nil {
		return nil
	}
	out := map[string]bool{}
	for p, ops := range doc.Paths {
		for m := range ops {
			if method := strings.ToUpper(m); httpMethods[method] {
				out[method+" "+NormalizePath(JoinPath(doc.BasePath, p))] = true
			}
		}
	}
	return out
}

func skipDir(name string) bool {
	switch name {
	case "node_modules", "vendor", ".git", ".venv", "venv", "__pycache__", "dist", "build", "target", ".next", ".foreman", "testdata":
		return true
	}
	return strings.HasPrefix(name, ".") && name != "."
}
