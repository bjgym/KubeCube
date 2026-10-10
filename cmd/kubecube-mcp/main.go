/*
Copyright 2021 KubeCube Authors

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

// Command kubecube-mcp serves the Model Context Protocol surface an external
// agent reaches the platform through.
//
// It holds no cluster credential. The tools it advertises call the KubeCube API
// with the session's token, and the platform decides what may happen; the
// surface only narrows what a session may ask for.
//
// Two pieces are not wired yet, and the binary says so at startup rather than
// pretending: the client that calls the platform, and the verification that
// turns a request's token into a session. Until they exist every tool call is
// refused, which is the right way round for a boundary.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/kubecube-io/kubecube/pkg/mcp"
	"github.com/kubecube-io/kubecube/pkg/utils/env"
)

func main() {
	var (
		bindAddr    = flag.String("bind-address", ":8090", "address to serve the MCP endpoint on")
		platformURL = flag.String("platform-url", os.Getenv("KUBECUBE_PLATFORM_URL"), "base URL of the KubeCube API the tools call")
		version     = flag.String("version", "dev", "version reported to clients")
	)
	flag.Parse()

	verifier := mcp.Verifier{Secret: env.JwtSecret()}

	server := &mcp.Server{
		Name:        "kubecube-mcp",
		Version:     *version,
		SessionFrom: verifier.Session,
	}

	mux := http.NewServeMux()
	mux.Handle("/mcp", server)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	log.Printf("kubecube-mcp %s listening on %s", *version, *bindAddr)
	if env.JwtSecret() == "" {
		log.Print("no signing secret is configured, so no session can be established and every tool call is refused")
	}
	if *platformURL == "" {
		log.Print("no platform URL is configured, so the tools have nothing to call yet")
	} else {
		log.Printf("platform URL %s is configured, but the client that calls it is not built yet", *platformURL)
	}

	httpServer := &http.Server{
		Addr:              *bindAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatalf("serving the MCP endpoint: %v", err)
	}
}
