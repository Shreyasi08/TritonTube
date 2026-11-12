package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Shreyasi08/TritonTube/internal/cluster"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("admin", flag.ContinueOnError)
	endpoint := flags.String("url", "http://127.0.0.1:8080", "web server URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	args = flags.Args()
	if len(args) < 1 {
		return fmt.Errorf("usage: admin [-url URL] list | health | migration | add ID HOST:PORT | remove ID")
	}
	method, path := http.MethodGet, ""
	var body io.Reader
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return fmt.Errorf("list takes no arguments")
		}
		path = "/api/cluster/nodes"
	case "health":
		if len(args) != 1 {
			return fmt.Errorf("health takes no arguments")
		}
		path = "/api/cluster/health"
	case "migration":
		if len(args) != 1 {
			return fmt.Errorf("migration takes no arguments")
		}
		path = "/api/cluster/migration"
	case "add":
		if len(args) != 3 {
			return fmt.Errorf("usage: admin add ID HOST:PORT")
		}
		method = http.MethodPost
		path = "/api/cluster/nodes"
		data, err := json.Marshal(cluster.Node{ID: args[1], Address: args[2]})
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	case "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: admin remove ID")
		}
		method = http.MethodDelete
		path = "/api/cluster/nodes/" + url.PathEscape(args[1])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	req, err := http.NewRequest(method, strings.TrimRight(*endpoint, "/")+path, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 11 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err = json.Indent(&pretty, data, "", "  "); err != nil {
		return fmt.Errorf("invalid server response: %s", data)
	}
	if _, err = fmt.Fprintln(out, pretty.String()); err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	if args[0] == "add" || args[0] == "remove" {
		var result cluster.Migration
		if err = json.Unmarshal(data, &result); err != nil {
			return err
		}
		if result.State != "committed" {
			return fmt.Errorf("membership change was not committed (state %q): %s", result.State, result.Error)
		}
	}
	if args[0] == "health" {
		var checks []cluster.NodeHealth
		if err = json.Unmarshal(data, &checks); err != nil {
			return err
		}
		for _, check := range checks {
			if !check.Healthy {
				return fmt.Errorf("node %s is unhealthy", check.Node.ID)
			}
		}
	}
	return nil
}
