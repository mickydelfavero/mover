// mover
// (C) 2019 Micky Del Favero micky@BeeCloudy.net

package main

import (
	"flag"
	"io/ioutil"
	"log"
	"log/syslog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fsnotify/fsnotify"
)

var watcher *fsnotify.Watcher

type tomlConfig struct {
	Movall bool
	Source string
	Destin string
	Recurs bool
	Fileop string
	Regexp string
	Logtag string
	Syslog bool
	Owrite bool
	Delay  int
	Uid    int
	Gid    int
}

func walkWatcher(path string, ofinfo os.FileInfo, err error) error {
	if ofinfo.Mode().IsDir() {
		return watcher.Add(path)
	}
	return nil
}

func createDirectory(path string, config tomlConfig) bool {

	log.Print("mkdir path: ", path)

	file, err := os.Stat(path)

	if os.IsNotExist(err) {
		err := os.MkdirAll(path, 0755)
		if err != nil {
			log.Print("Error creating directory ", path, ":", err)
			return false
		}
		if config.Uid != -1 && config.Gid != -1 {
			relPath, _ := filepath.Rel(config.Destin, path)
			dirPath := filepath.Dir(relPath)
			firstDir := filepath.Join(config.Destin, strings.Split(dirPath, string(os.PathSeparator))[0])

			if dirPath == "." {
				firstDir = path
			}

			err := filepath.Walk(firstDir, func(name string, info os.FileInfo, err error) error {
				if err != nil {
					log.Print("Error: ", err)
					return err
				}
				log.Print("Chowning ", name, " to ", config.Uid, ":", config.Gid)
				err = os.Chown(name, config.Uid, config.Gid)
				if err != nil {
					log.Print("Error chowning file: ", err)
					return err
				}
				return nil
			})
			if err != nil {
				log.Print("Error: ", err)
				return false
			}
		}
		return true
	}

	if file.Mode().IsRegular() {
		log.Print(path, " already exist as a file!")
		return false
	}
	return true
}

func chkFileExist(path string, err error) bool {
	if os.IsNotExist(err) {
		return false
	}
	return true
}

func chkFileRegexp(path string, re string) bool {
	// regexp
	tf, _ := regexp.MatchString(re, path)
	return tf
}

func mover(path string, config tomlConfig) {
	relPath, _ := filepath.Rel(config.Source, path)
	dirPath := filepath.Dir(relPath)
	fileName := filepath.Base(path)
	destPath := filepath.Join(config.Destin, dirPath)
	destName := filepath.Join(destPath, fileName)

	if !createDirectory(destPath, config) {
		return
	}

	_, err := os.Stat(destName)
	if chkFileExist(destName, err) {
		if !config.Owrite {
			log.Print(destName, " already exists, exiting")
			return
		} else {
			log.Print(destName, " already exists, overwriting it")
		}
	}

	log.Print("Moving ", path, " to ", destName)
	err = os.Rename(path, destName)
	if err != nil {
		log.Print("Error moving file: ", err)
		return
	}

	if config.Uid != -1 && config.Gid != -1 {
		log.Print("Chowning ", destName, " to ", config.Uid, ":", config.Gid)
		err = os.Chown(destName, config.Uid, config.Gid)
		if err != nil {
			log.Print("Error chowning file: ", err)
			return
		}
	}
}

// main
func main() {
	var cfgfile string

	flag.StringVar(&cfgfile, "c", "/etc/mover/mover.toml", "file config")
	flag.StringVar(&cfgfile, "conf", "/etc/mover/mover.toml", "file config")
	flag.Parse()

	var config tomlConfig
	if _, err := toml.DecodeFile(cfgfile, &config); err != nil {
		log.Print(err)
		return
	}

	if config.Syslog {
		logger, err := syslog.New(syslog.LOG_NOTICE, config.Logtag)
		if err == nil {
			// no timestamp https://stackoverflow.com/questions/48629988/remove-timestamp-prefix-from-go-logger
			log.SetFlags(0)
			log.SetOutput(logger)
		}
	}

	log.Print("Started!")

	if config.Movall {
		log.Print("Searching existing files in ", config.Source)

		lsfile := make([]string, 0)

		if config.Recurs {
			err := filepath.Walk(config.Source, func(path string, ofinfo os.FileInfo, err error) error {
				if ofinfo.Mode().IsRegular() {
					log.Print("Finded file: ", path)
					lsfile = append(lsfile, path)
				}
				return nil
			})
			if err != nil {
				log.Print("ERROR: ", err)
			}
		} else {
			ls, err := ioutil.ReadDir(config.Source)
			if err != nil {
				log.Print("ERROR: ", err)
			}

			for _, f := range ls {
				if !f.IsDir() {
					fname := filepath.Join(config.Source, f.Name())
					log.Print("Finded file: ", fname)
					lsfile = append(lsfile, fname)
				}
			}
		}

		for _, f := range lsfile {

			if !chkFileRegexp(f, config.Regexp) {
				log.Print(f, " not matchin regexp ", config.Regexp, " exiting")
				continue
			}

			_, err := os.Stat(f)
			if !chkFileExist(f, err) {
				log.Print(f, " disappeared")
				continue
			}

			mover(f, config)
		}

	}

	log.Print("Start watching ", config.Source)
	watcher, _ = fsnotify.NewWatcher()
	defer func() {
		watcher.Close()
		log.Print("That's all, folks!")
	}()

	if config.Recurs {
		if err := filepath.Walk(config.Source, walkWatcher); err != nil {
			log.Print("ERROR: ", err)
		}
	} else {
		watcher.Add(config.Source)
	}

	var fsop fsnotify.Op

	switch config.Fileop {
	case "CREATE":
		fsop = fsnotify.Create
	case "WRITE":
		fsop = fsnotify.Write
	case "REMOVE":
		fsop = fsnotify.Remove
	case "RENAME":
		fsop = fsnotify.Rename
	case "CHMOD":
		fsop = fsnotify.Chmod
	}

	done := make(chan bool)

	// loop
	go func() {
		for {
			select {
			case event := <-watcher.Events:
				if event.Op&fsop != 0 {
					// goroutine => non bloccante
					go func() {
						log.Print("New event ", config.Fileop, event.Name)

						var (
							// fileStat os.FileInfo
							err error
						)

						if _, err = os.Stat(event.Name); err != nil {
							if !chkFileExist(event.Name, err) {
								log.Print(event.Name, " disappeared")
								return
							}
						}

						fileLstat, err := os.Lstat(event.Name)
						if err != nil {
							log.Print("Err: ", err)
						}

						switch mode := fileLstat.Mode(); {
						case mode&os.ModeSymlink != 0:
							log.Print(event.Name, " is a symlink, exiting")
							return
						case mode.IsDir():
							if config.Recurs {
								log.Print("Starting to watch: ", event.Name)
								watcher.Add(event.Name)
							}
						case mode.IsRegular():
							if !chkFileRegexp(event.Name, config.Regexp) {
								log.Print(event.Name, " not matchin regexp ", config.Regexp, " exiting")
								return
							}

							log.Print("Waiting: ", config.Delay)
							time.Sleep(time.Duration(config.Delay) * time.Second)

							_, err := os.Stat(event.Name)
							if !chkFileExist(event.Name, err) {
								log.Print(event.Name, " disappeared")
								return
							}

							mover(event.Name, config)
						}
					}()
				}

				if event.Op&fsnotify.Remove != 0 {
					watcher.Remove(event.Name)
				}

			// errors
			case err := <-watcher.Errors:
				log.Print("ERROR", err)
			}
		}
	}()

	<-done
}
