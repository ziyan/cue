package upgrade

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeDocker is enough of the Docker daemon to test the swap: it keeps
// containers in a map and answers the six calls the swap makes.
//
// A real daemon would be a better test and is not one that can run here: the
// image has no Docker in it, CI has no nested daemon, and a test that needs
// one would be skipped everywhere and therefore prove nothing. What matters
// about the swap is the order it does things in and what it does when a step
// fails, and both are visible from this side of the socket.
type fakeDocker struct {
	mutex      sync.Mutex
	containers map[string]*fakeContainer
	next       int

	// Made to fail on purpose.
	failCreate bool
	failStart  string // the image whose containers refuse to start

	// Everything that happened, in order, so that a test can insist the old
	// container was not destroyed before the new one answered.
	events []string
}

type fakeContainer struct {
	id      string
	name    string
	image   string
	running bool
	config  json.RawMessage
	host    json.RawMessage
}

func newFakeDocker(t *testing.T) (*fakeDocker, *Docker) {
	t.Helper()

	fake := &fakeDocker{containers: map[string]*fakeContainer{}}

	// A Unix socket in a temporary directory, because that is what the real
	// thing is and the client dials one by path.
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("cannot listen on a unix socket: %s", err)
	}

	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: fake},
	}
	server.Start()
	t.Cleanup(server.Close)

	return fake, NewDocker(socket)
}

func (self *fakeDocker) add(name, image string, running bool) *fakeContainer {
	self.mutex.Lock()
	defer self.mutex.Unlock()

	self.next++
	container := &fakeContainer{
		id:      fmt.Sprintf("%064d", self.next),
		name:    name,
		image:   image,
		running: running,
		config:  json.RawMessage(`{"Hostname":"whatever","Env":["CUE_CONFIG=/etc/cue/cue.yaml"],"Cmd":["run"]}`),
		host:    json.RawMessage(`{"NetworkMode":"host","Binds":["/etc/cue:/etc/cue"],"CapAdd":["NET_ADMIN"]}`),
	}
	self.containers[container.id] = container
	return container
}

func (self *fakeDocker) find(reference string) *fakeContainer {
	if container, found := self.containers[reference]; found {
		return container
	}
	for _, container := range self.containers {
		if container.name == reference {
			return container
		}
	}
	return nil
}

func (self *fakeDocker) record(what string) {
	self.events = append(self.events, what)
}

// happened reports whether one event came before another.
func (self *fakeDocker) happened(first, second string) bool {
	firstAt, secondAt := -1, -1
	for index, event := range self.events {
		if strings.HasPrefix(event, first) && firstAt < 0 {
			firstAt = index
		}
		if strings.HasPrefix(event, second) && secondAt < 0 {
			secondAt = index
		}
	}
	return firstAt >= 0 && secondAt >= 0 && firstAt < secondAt
}

func (self *fakeDocker) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	self.mutex.Lock()
	defer self.mutex.Unlock()

	path := request.URL.Path
	if at := strings.Index(path, "/v1."); at == 0 {
		path = path[strings.Index(path[1:], "/")+1:]
	}
	response.Header().Set("Content-Type", "application/json")

	switch {
	case path == "/_ping":
		_, _ = response.Write([]byte("OK"))

	case strings.HasPrefix(path, "/images/create"):
		self.record("pull " + request.URL.Query().Get("fromImage") + ":" + request.URL.Query().Get("tag"))
		_, _ = response.Write([]byte(`{"status":"Downloaded"}`))

	case strings.HasPrefix(path, "/containers/create"):
		if self.failCreate {
			response.WriteHeader(http.StatusInternalServerError)
			_, _ = response.Write([]byte(`{"message":"no room"}`))
			return
		}
		var body struct {
			Image string `json:"Image"`
		}
		_ = json.NewDecoder(request.Body).Decode(&body)
		name := request.URL.Query().Get("name")
		self.next++
		container := &fakeContainer{
			id:    fmt.Sprintf("%064d", self.next),
			name:  name,
			image: body.Image,
		}
		self.containers[container.id] = container
		self.record("create " + name + " from " + body.Image)
		_, _ = fmt.Fprintf(response, `{"Id":%q}`, container.id)

	case strings.HasSuffix(path, "/start"):
		container := self.find(containerIn(path))
		if container == nil {
			response.WriteHeader(http.StatusNotFound)
			_, _ = response.Write([]byte(`{"message":"no such container"}`))
			return
		}
		if self.failStart != "" && container.image == self.failStart {
			self.record("start refused " + container.name)
			response.WriteHeader(http.StatusInternalServerError)
			_, _ = response.Write([]byte(`{"message":"it will not start"}`))
			return
		}
		container.running = true
		self.record("start " + container.name)
		response.WriteHeader(http.StatusNoContent)

	case strings.HasSuffix(path, "/stop"):
		if container := self.find(containerIn(path)); container != nil {
			container.running = false
			self.record("stop " + container.name)
		}
		response.WriteHeader(http.StatusNoContent)

	case strings.Contains(path, "/rename"):
		container := self.find(containerIn(path))
		if container == nil {
			response.WriteHeader(http.StatusNotFound)
			_, _ = response.Write([]byte(`{"message":"no such container"}`))
			return
		}
		to := request.URL.Query().Get("name")
		self.record("rename " + container.name + " -> " + to)
		container.name = to
		response.WriteHeader(http.StatusNoContent)

	case strings.HasSuffix(path, "/json"):
		container := self.find(containerIn(path))
		if container == nil {
			response.WriteHeader(http.StatusNotFound)
			_, _ = response.Write([]byte(`{"message":"no such container"}`))
			return
		}
		_ = json.NewEncoder(response).Encode(map[string]interface{}{
			"Id":         container.id,
			"Name":       "/" + container.name,
			"Image":      container.image,
			"Config":     container.config,
			"HostConfig": container.host,
			"State":      map[string]interface{}{"Running": container.running},
		})

	case request.Method == http.MethodDelete:
		container := self.find(containerIn(path))
		if container == nil {
			response.WriteHeader(http.StatusNotFound)
			_, _ = response.Write([]byte(`{"message":"no such container"}`))
			return
		}
		self.record("remove " + container.name)
		delete(self.containers, container.id)
		response.WriteHeader(http.StatusNoContent)

	default:
		response.WriteHeader(http.StatusNotFound)
		_, _ = response.Write([]byte(`{"message":"the fake daemon does not answer ` + path + `"}`))
	}
}

// containerIn pulls the container out of /containers/<what>/something.
func containerIn(path string) string {
	pieces := strings.Split(strings.TrimPrefix(path, "/containers/"), "/")
	return pieces[0]
}
