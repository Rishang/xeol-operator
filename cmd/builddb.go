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
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Rishang/xeol-operator/internal/eoldb"
)

// buildDB implements `xeol-operator build-db`: build xeol's EOL DB from endoflife.date,
// e.g. on a connected machine for an air-gapped cluster's mounted volume.
func buildDB(args []string) int {
	fs := flag.NewFlagSet("build-db", flag.ExitOnError)
	out := fs.String("out", "db", "Directory to write; the DB lands in <out>/1/.")
	from := fs.String("from", "", "Read a saved endoflife.date products/full JSON instead of downloading it.")
	url := fs.String("url", eoldb.DefaultURL, "endoflife.date products/full URL.")
	_ = fs.Parse(args)

	var data []byte
	var err error
	if *from != "" {
		data, err = os.ReadFile(*from)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		data, err = eoldb.Fetch(ctx, http.DefaultClient, *url)
	}
	if err == nil {
		err = eoldb.Build(data, *out, time.Now())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "build-db:", err)
		return 1
	}
	fmt.Printf("EOL database written to %s/1\n", *out)
	return 0
}
