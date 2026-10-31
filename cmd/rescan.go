/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Rishang/xeol-operator/internal/api"
)

// defaultAdminAddr is the loopback listener `manager rescan` talks to (--admin-bind-address).
const defaultAdminAddr = "127.0.0.1:8082"

// rescanCmd implements `manager rescan [--addr host:port] [image glob...]`: queue a rescan of the
// running images matching the globs (all without globs). Run it in the operator's pod:
//
//	kubectl -n xeol-system exec deploy/xeol-operator -- /manager rescan 'python:3.8*'
func rescanCmd(args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("rescan", flag.ContinueOnError)
	fs.SetOutput(errOut)
	addr := fs.String("addr", defaultAdminAddr, "The operator's admin listener (--admin-bind-address).")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(errOut, "usage: manager rescan [--addr host:port] [image glob...]\n\n"+
			"Queue an immediate rescan of the running images matching the globs (excludeImages syntax,\n"+
			"e.g. 'python:3.8*' or 'registry.internal/*'); every running image without globs.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	body, _ := json.Marshal(api.RescanRequest{Images: fs.Args()})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+*addr+"/rescan", bytes.NewReader(body))
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "rescan:", err)
		return 1
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "rescan: is the operator running in this pod?", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		_, _ = fmt.Fprintf(errOut, "rescan: %s: %s\n", resp.Status, strings.TrimSpace(string(b)))
		return 1
	}
	var res api.RescanResult
	if err := json.Unmarshal(b, &res); err != nil {
		_, _ = fmt.Fprintln(errOut, "rescan: bad response:", err)
		return 1
	}
	if len(res.Images) == 0 {
		_, _ = fmt.Fprintln(out, "no running image matches")
		return 0
	}
	_, _ = fmt.Fprintf(out, "queued %d image(s) for a rescan:\n", len(res.Images))
	for _, ref := range res.Images {
		_, _ = fmt.Fprintln(out, "  "+ref)
	}
	return 0
}

// loopbackOnly rejects an admin address that isn't on loopback: the listener has no auth.
func loopbackOnly(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("must be a loopback address (127.0.0.1, ::1 or localhost): the admin listener has no auth")
	}
	return nil
}

// adminServer serves the admin listener on every replica (a standby answers 503 itself).
type adminServer struct {
	addr    string
	handler http.Handler
}

func (s adminServer) Start(ctx context.Context) error {
	srv := &http.Server{Addr: s.addr, Handler: s.handler, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (adminServer) NeedLeaderElection() bool { return false }
