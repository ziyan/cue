package config

import (
	"fmt"
	"sync"

	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("config")

// Store owns the configuration file. It hands out immutable snapshots, so a
// component that read the configuration a moment ago cannot have it change
// underneath it, and it applies every change through the same path whether it
// came from a person editing the file or from the web interface.
//
// Watchers are told after a change has been accepted and written, never
// before, so nothing ever acts on a configuration that failed to save.
type Store struct {
	filename string

	mutex   sync.RWMutex
	current *Configuration

	watchersMutex sync.Mutex
	watchers      []chan *Configuration
}

// Open reads the configuration file and returns a store over it.
func Open(filename string) (*Store, error) {
	configuration, err := Load(filename)
	if err != nil {
		return nil, err
	}
	return &Store{filename: filename, current: configuration}, nil
}

// OpenWith returns a store over a configuration that is already in hand,
// which is what the tests use.
func OpenWith(filename string, configuration *Configuration) *Store {
	return &Store{filename: filename, current: configuration}
}

// Filename is the file this store reads and writes.
func (self *Store) Filename() string {
	return self.filename
}

// Current returns the configuration in force. The value must not be modified;
// use Update to change anything.
func (self *Store) Current() *Configuration {
	self.mutex.RLock()
	defer self.mutex.RUnlock()
	return self.current
}

// Update applies a change. The function is given a copy to modify; if it
// returns an error, or if the result does not validate, nothing is written and
// the configuration in force is untouched.
func (self *Store) Update(change func(configuration *Configuration) error) error {
	self.mutex.Lock()
	previous := self.current
	updated := previous.Clone()
	if err := change(updated); err != nil {
		self.mutex.Unlock()
		return err
	}
	RestoreSecrets(updated, previous)
	updated.Normalize()
	if err := updated.Validate(); err != nil {
		self.mutex.Unlock()
		return err
	}
	if err := updated.Save(self.filename); err != nil {
		self.mutex.Unlock()
		return err
	}
	self.current = updated
	self.mutex.Unlock()

	self.notify(updated)
	return nil
}

// Reload re-reads the file, which is what SIGHUP does. A file that no longer
// validates is refused and the configuration in force is kept: a display that
// carries on showing yesterday's dashboard is better than one that goes black
// because somebody mistyped a duration over a slow SSH connection.
func (self *Store) Reload() error {
	reloaded, err := Load(self.filename)
	if err != nil {
		return fmt.Errorf("config: keeping the configuration already in force: %w", err)
	}

	self.mutex.Lock()
	self.current = reloaded
	self.mutex.Unlock()

	log.Noticef("reloaded %s", self.filename)
	self.notify(reloaded)
	return nil
}

// Watch returns a channel that receives the configuration after every accepted
// change. The channel has room for one value and a send that would block is
// dropped, because a slow watcher must not stop the daemon from applying a
// change; the value dropped is always older than the one already queued.
func (self *Store) Watch() <-chan *Configuration {
	self.watchersMutex.Lock()
	defer self.watchersMutex.Unlock()
	watcher := make(chan *Configuration, 1)
	self.watchers = append(self.watchers, watcher)
	return watcher
}

func (self *Store) notify(configuration *Configuration) {
	self.watchersMutex.Lock()
	defer self.watchersMutex.Unlock()
	for _, watcher := range self.watchers {
		select {
		case watcher <- configuration:
		default:
			// The watcher has not read the previous change yet. It will see
			// this one when it does, because what is queued is replaced.
			select {
			case <-watcher:
			default:
			}
			select {
			case watcher <- configuration:
			default:
			}
		}
	}
}
