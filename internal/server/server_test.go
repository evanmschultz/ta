package server

import (
	"testing"
)

// TestServe_NewConfigDefaults verifies that New() correctly preserves the
// config passed to it. Regression guard for the config storage seam.
func TestServe_NewConfigDefaults(t *testing.T) {
	tests := []struct {
		name     string
		bind     string
		port     int
		wantBind string
		wantPort int
	}{
		{
			name:     "explicit config values",
			bind:     "0.0.0.0",
			port:     8080,
			wantBind: "0.0.0.0",
			wantPort: 8080,
		},
		{
			name:     "localhost default",
			bind:     "127.0.0.1",
			port:     4321,
			wantBind: "127.0.0.1",
			wantPort: 4321,
		},
		{
			name:     "any address, high port",
			bind:     "::",
			port:     65535,
			wantBind: "::",
			wantPort: 65535,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{Bind: tt.bind, Port: tt.port}
			srv := New(cfg)

			if got, want := srv.config.Bind, tt.wantBind; got != want {
				t.Fatalf("srv.config.Bind = %q, want %q", got, want)
			}
			if got, want := srv.config.Port, tt.wantPort; got != want {
				t.Fatalf("srv.config.Port = %d, want %d", got, want)
			}
		})
	}
}

// TestServe_NewRegistersBaseMux verifies that New() constructs and stores
// an HTTP mux. Regression guard for the mux seam that later route droplets
// will extend.
func TestServe_NewRegistersBaseMux(t *testing.T) {
	cfg := Config{Bind: "127.0.0.1", Port: 4321}
	srv := New(cfg)

	if srv.mux == nil {
		t.Fatalf("srv.mux is nil, expected a non-nil *http.ServeMux")
	}
}
