package main

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"path"
	"time"

	"github.com/hpcloud/tail"
	"github.com/justnoise/fsnotify"
	uuid "github.com/satori/go.uuid"
)

const maxUnits = 10

var (
	units   map[string]Unit
	baseDir string
)

type Unit struct {
	Name string
}

type Switchboard struct {
	watcher *fsnotify.Watcher
}

// have a go routine that creates units
func main() {
	units = make(map[string]Unit)
	baseDir = "/home/bcox/go/src/github.com/justnoise/switchboard/"
	unitCreator()
	watcher, err := fsnotify.NewWatcher(fsnotify.Open | fsnotify.Close)
	if err != nil {
		log.Fatal(err)
	}
	defer watcher.Close()

	s := Switchboard{watcher: watcher}

	go unitReader()
	s.Start()
}

func (s *Switchboard) Start() {
	go s.fileCreator()
	time.Sleep(1 * time.Second)
	s.runSwitchboard()
}

func (s *Switchboard) runSwitchboard() {
	// listen for file changes: when we get an open, pump logs to the
	// file we have to watch out of FS notifications from this process
	// opening the file, also watch for the file being opened multiple
	// times.  So keep a count of opens and close when we're the only
	// ones who have the file open
	quitChans := make(map[string]chan struct{})
	openCount := make(map[string]int)
	for {
		select {
		case event := <-s.watcher.Events:
			fmt.Printf("Got OP %v for %s\n", event.Op, event.Name)
			if event.Op&fsnotify.Open == fsnotify.Open {
				ct := openCount[event.Name]
				if ct == 0 {
					// prevent races on close by opening the file here
					uid := uuid.NewV4().String()
					f, err := os.OpenFile(
						event.Name, os.O_WRONLY|os.O_APPEND, 0666)
					if err != nil {
						fmt.Println("Error opening file for pumping logs", event.Name)
						continue
					}

					c := make(chan struct{}, 1)
					quitChans[event.Name] = c
					go pumpLogs(uid, f, event.Name, c)
				}
				openCount[event.Name] += 1
				fmt.Println(openCount[event.Name])
			}
			if event.Op&fsnotify.Close == fsnotify.Close {
				ct := openCount[event.Name]
				c, exists := quitChans[event.Name]
				fmt.Println("closing", event.Name, ct, exists)
				if !exists {
					// got ourselves closing the file, this is OK
					delete(openCount, event.Name)
					continue
				}
				openCount[event.Name] -= 1
				if openCount[event.Name] <= 1 {
					c <- struct{}{}
					delete(quitChans, event.Name)
					delete(openCount, event.Name)
				}
			}
		}
	}
}

func pumpLogs(uid string, f *os.File, fullPath string, quit chan struct{}) {
	fmt.Println(uid, "pumping logs to", fullPath)
	t := time.NewTicker(1 * time.Second)
	for {
		select {
		case <-quit:
			fmt.Println(uid, "Stopping pumping logs for", fullPath)
			err := f.Truncate(0)
			if err != nil {
				fmt.Println("Error truncating file", err)
			}
			_ = f.Close()
			t.Stop()
			return
		case <-t.C:
			msg := fmt.Sprintf("%s, Logs for %s at %v\n", uid, fullPath, time.Now())
			_, err := f.Write([]byte(msg))
			if err != nil {
				fmt.Println("Error writing to logfile", err)
			}
		}
	}
}

// Todo: this should be something that randomly creates and deletes units
func unitCreator() {
	//t := time.NewTicker(1 * time.Second)
	for i := 0; i < maxUnits; i++ {
		unitName := fmt.Sprintf("%d", i)
		units[unitName] = Unit{Name: unitName}
		fmt.Println("Created unit:", unitName)
	}
}

func diffUnit(a, b map[string]Unit) []Unit {
	d := []Unit{}
	for k, u := range a {
		_, exists := b[k]
		if !exists {
			d = append(d, u)
		}
	}
	return d
}

// watches list of units and creates files on the FS for those units
// also creates file watches.
func (s *Switchboard) fileCreator() {
	knownUnits := map[string]Unit{}
	//t := time.NewTicker(1 * time.Second)
	for {
		<-time.After(1 * time.Second)
		add := diffUnit(units, knownUnits)
		del := diffUnit(knownUnits, units)
		for _, u := range add {
			p := path.Join(baseDir, u.Name)
			_, err := os.Create(p)
			if err != nil {
				panic(err)
			}
			knownUnits[u.Name] = u
			_ = s.watcher.Add(p)
			fmt.Println("watching", p)
		}
		for _, u := range del {
			p := path.Join(baseDir, u.Name)
			_ = s.watcher.Remove(p)
			err := os.Remove(p)
			if err != nil {
				panic(err)
			}
			delete(knownUnits, u.Name)
		}
	}
}

// Randomly opens a unit and reads for a bit of time until it decides
// it is done.  Kinda like a user asking for the logs
func unitReader() {
	time.Sleep(3 * time.Second)
	for {
		n := rand.Intn(len(units))
		i := 0
		var unit Unit
		for _, v := range units {
			if i == n {
				unit = v
				break
			}
			i += 1
		}
		if unit.Name == "" {
			fmt.Println("No units yet")
			time.Sleep(1 * time.Second)
			continue
		}
		unit = units["7"]
		readTime := time.Duration(rand.Intn(3)) * time.Second
		start := time.Now()
		fmt.Printf("tailing file %s for %v seconds\n", unit.Name, readTime.Seconds())
		t, err := tail.TailFile(unit.Name, tail.Config{Follow: true})
		if err != nil {
			fmt.Println("Error following file", unit.Name)
		}
		for line := range t.Lines {
			fmt.Println(unit.Name, "---", line.Text)
			if time.Now().After(start.Add(readTime)) {
				break
			}
		}
		err = t.Stop()
		if err != nil {
			fmt.Println("Error stopping the tail of file", unit.Name)
		}
	}
}
