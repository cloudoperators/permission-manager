// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Greenhouse contributors
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package shared

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Per-command timeouts so a hung tool fails fast instead of stalling the whole
// synchronized bootstrap until the outer ginkgo --timeout.
const (
	defaultCmdTimeout = 5 * time.Minute
	buildCmdTimeout   = 15 * time.Minute
)

// run executes a command with the default timeout and returns its stdout.
func run(name string, args ...string) (string, error) {
	return runCtx(defaultCmdTimeout, name, args...)
}

// runCtx runs a command under a timeout. stderr is captured separately and
// folded into the error, so tool warnings never corrupt parsed output.
func runCtx(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return stdout.String(), fmt.Errorf("%s %s: timed out after %s\n%s", name, strings.Join(args, " "), timeout, stderr.String())
	}
	if err != nil {
		return stdout.String(), fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

// runInput feeds stdin to a command (piping manifests into `kubectl apply -f -`).
func runInput(stdin, name string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaultCmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("%s %s: timed out after %s\n%s", name, strings.Join(args, " "), defaultCmdTimeout, stderr.String())
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, stderr.String())
	}
	return nil
}

// Preflight verifies the external tooling the suite shells out to is present.
func Preflight() error {
	for _, check := range [][]string{
		{"kind", "--version"},
		{"kubectl", "version", "--client"},
		{"helm", "version", "--short"},
		{"docker", "info"},
	} {
		if _, err := run(check[0], check[1:]...); err != nil {
			return fmt.Errorf("preflight %q failed (is it installed and running?): %w", check[0], err)
		}
	}
	return nil
}

// kindContext returns the kubectl context name kind assigns to a cluster.
func kindContext(cluster string) string {
	return "kind-" + cluster
}

// ClusterExists reports whether a kind cluster with the given name exists.
func ClusterExists(cluster string) bool {
	out, err := run("kind", "get", "clusters")
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == cluster {
			return true
		}
	}
	return false
}

// CreateCluster creates the kind cluster from the suite's kind-config. It is
// idempotent: an existing cluster is reused.
func CreateCluster(cluster, kindConfigPath string) error {
	if ClusterExists(cluster) {
		return nil
	}
	_, err := runCtx(buildCmdTimeout, "kind", "create", "cluster",
		"--name", cluster,
		"--config", kindConfigPath,
		"--wait", "90s",
	)
	return err
}

// DeleteCluster tears down the kind cluster. Safe to call when it is absent.
func DeleteCluster(cluster string) error {
	_, err := run("kind", "delete", "cluster", "--name", cluster)
	return err
}

// HostKubeconfig returns the host-reachable kubeconfig for the kind cluster
// (server on 127.0.0.1). The test process uses this to talk to the API server.
func HostKubeconfig(cluster string) (string, error) {
	return run("kind", "get", "kubeconfig", "--name", cluster)
}

// InternalKubeconfig returns the in-docker-network kubeconfig for the kind
// cluster (server on the control-plane container name). This is what the
// controller pod uses to reach the downstream API server, so it is the value
// stored in the onboarding Secret.
func InternalKubeconfig(cluster string) (string, error) {
	return run("kind", "get", "kubeconfig", "--name", cluster, "--internal")
}

// BuildAndLoadImage builds the controller image from the repo Dockerfile and
// loads it into the kind cluster. Skipped when E2E_SKIP_BUILD=1.
func BuildAndLoadImage(cluster, repoRoot string) error {
	if os.Getenv(EnvSkipBuild) == "1" {
		return nil
	}
	if _, err := runCtx(buildCmdTimeout, "docker", "build", "-t", ControllerImage, "-f", repoRoot+"/Dockerfile", repoRoot); err != nil {
		return fmt.Errorf("docker build: %w", err)
	}
	if _, err := runCtx(buildCmdTimeout, "kind", "load", "docker-image", ControllerImage, "--name", cluster); err != nil {
		return fmt.Errorf("kind load: %w", err)
	}
	return nil
}
