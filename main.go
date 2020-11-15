package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const basePort = 8080

func main() {
	var gitDir string
	deployments := make(map[string]int)

	flag.StringVar(&gitDir, "directory", "", "The git directory from which to build images.")
	flag.Parse()

	if gitDir == "" {
		fmt.Fprintln(os.Stderr, "Please specify a directory.")
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", rootHandler(deployments))
	mux.HandleFunc("/doxy", doxyHandler(gitDir, deployments))

	http.ListenAndServe(":5000", mux)
}

func rootHandler(deployments map[string]int) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		subdomain := strings.TrimSuffix(host, ".telltale.xyz")

		if subdomain == "" || subdomain == "telltale.xyz" {
			http.Error(w, "No deployment prefix found.", 400)
			return
		}

		if _, ok := deployments[subdomain]; ok {
			port := basePort + deployments[subdomain]
			url, _ := url.Parse("http://localhost:" + strconv.Itoa(port))
			proxy := httputil.NewSingleHostReverseProxy(url)
			proxy.ServeHTTP(w, r)

		} else {
			http.Error(w, "Deployment "+subdomain+" not found.", 400)
		}

		return
	}
}

func doxyHandler(gitDir string, deployments map[string]int) func(w http.ResponseWriter, r *http.Request) {
	imageNumber := 0

	return func(w http.ResponseWriter, r *http.Request) {
		var doxyRequest DoxyRequest

		err := json.NewDecoder(r.Body).Decode(&doxyRequest)

		if err != nil {
			http.Error(w, "Could not decode JSON body.", 400)
			return
		}

		// Validate the request, starting with DeploymentName since it's mandatory.
		if doxyRequest.DeploymentName == "" {
			http.Error(w, "No deployment name specified.", 400)
			return
		}

		if doxyRequest.BranchName == "" {
			doxyRequest.BranchName = "master"
		}

		if doxyRequest.Dockerfile == "" {
			doxyRequest.Dockerfile = "Dockerfile"
		}

		if doxyRequest.HttpPort == 0 {
			doxyRequest.HttpPort = 80
		}

		if doxyRequest.Subdirectory == "" {
			doxyRequest.Subdirectory = "."
		}

		// Now a shell script to do all the things we need.
		script := "set -e\n"

		// First, change into the root directory:
		script += "cd " + gitDir + "\n"

		// If we need to pull from origin, do that next.
		if doxyRequest.PullOrigin {
			script += "git pull\n"
		}

		// Check out the requested branch
		script += "git checkout " + doxyRequest.BranchName + "\n"

		// Change into the requested subdirectory
		script += "cd " + strings.TrimPrefix(doxyRequest.Subdirectory, "/") + "\n"

		// Now build the image.
		tag := "image" + strconv.Itoa(imageNumber)
		script += "docker build -t " + tag + " -f " + doxyRequest.Dockerfile + " .\n"

		// Now run it, mapping the speficied HTTP port out to an available high port.
		hostPort := basePort + imageNumber
		script += "docker run -d -p " + strconv.Itoa(hostPort) + ":" + strconv.Itoa(doxyRequest.HttpPort) + " " + tag + "\n"

		fmt.Println(script)

		// Now we can actually execute the script.
		cmd := exec.Command("/bin/bash", "-c", script)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err = cmd.Run()
		if err != nil {
			fmt.Println(err)
			fmt.Println(stdout.String())
			fmt.Println(stderr.String())

			responseBody := "Error running command: " + err.Error() + "\n"
			responseBody += "shell command:\n" + script + "\n"
			responseBody += "stdout:\n" + stdout.String() + "\n"
			responseBody += "stderr:\n" + stderr.String()

			http.Error(w, responseBody, 500)
			return
		}

		fmt.Println(stdout.String())

		// Add the deployment to the map.
		deployments[doxyRequest.DeploymentName] = imageNumber

		// Increment the image number for next time.
		imageNumber++

		w.Write([]byte("Deployed!"))
		return
	}
}

type DoxyRequest struct {
	BranchName     string `json:"branchName"`
	Subdirectory   string `json:"subdirectory"`
	Dockerfile     string `json:"dockerfile"`
	DeploymentName string `json:"deploymentName"`
	HttpPort       int    `json:"httpPort"`
	PullOrigin     bool   `json:"pullOrigin"`
}

/* Doxy
 *
 * Initial setup
 * - Run the server with a directory containing a git repo as an argument.
 *
 * Using it
 * - Send it a web request with some info:
 *   > A branch name
 *   > A subdirectory
 *   > A Dockerfile name (default: Dockerfile)
 *   > A name to deploy under
 *   > What port to use for HTTP requests (default: 80)
 *   > Whether to pull from origin?
 * - It'll then:
 *   > Pull from origin (if asked?), then run docker build with the specified subdirectory and Dockerfile
 *   > Run the image in host mode, exposing ports as requested
 *   > Add a soft route handler for the deployment name
 *   > Route requests for /deployment-name/* upstream to the container on the chosen port
 */
