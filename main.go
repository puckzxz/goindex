package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"sync"

	"github.com/gammazero/workerpool"
	"github.com/karrick/godirwalk"
	"github.com/schollz/progressbar/v3"
)

// Simple function to return the system root
func getSysRoot() string {
	if runtime.GOOS == "windows" {
		return "C:\\"
	} else {
		return "/"
	}
}

var mu sync.Mutex

// Thread safe function to write to a file
// If we didn't have a mutex then runtime.NumCPU() threads would be trying to write in a file at the same time
func writeToFile(text string, f *os.File) {
	mu.Lock()
	f.WriteString(text)
	mu.Unlock()
}

func main() {
	// Open a file that we can write to
	handle, err := os.OpenFile("files.csv", os.O_WRONLY|os.O_CREATE, 0755)
	if err != nil {
		panic(err)
	}
	// Defer closing of the file until the end of main
	defer handle.Close()

	// Optional, start a workerpool with the amount of threads we have
	wp := workerpool.New(runtime.NumCPU())

	// Only needed if we setup a workerpool, you could do this on a single thread
	// I'm only using this so I can queue up a bunch of tasks and then execute them all at once
	ctx, cancel := context.WithCancel(context.Background())

	// Pause our workerpool so it won't immediately start executing items submitted to it
	wp.Pause(ctx)

	// Progress bar for indexing files, -1 sets this to indeterminate
	// We don't know how many files we'll be parsing, could be a single file or an entire drive
	indexBar := progressbar.Default(-1)

	// Progress bar for when we're hashing the files, this is a pointer so I can reference it before I actually initialize it.
	// Don't do this unless you know you'll initialize it before you use it or you'll panic
	var hashBar *progressbar.ProgressBar

	// Simple way to get command line flags in Go, there are other libraries that do this better but this is alright
	// A good exercise would be to allow me to pass a filename to the program using a flag
	walkDir := flag.String("walkDir", getSysRoot(), "The directory to walk, defaults to top most level directory")

	// Allows you to run .\goindex.exe -h
	flag.Usage = func() {
		flag.PrintDefaults()
	}

	// Parse any passed flags into the respective variables
	flag.Parse()

	// Write the CSV header into our file
	// You could change this to support more headers if you need them
	writeToFile("Path, Hash, Time\n", handle)

	err = godirwalk.Walk(*walkDir, &godirwalk.Options{
		// A callback function similar to the go stdlib filepath.WalkDir
		Callback: func(osPathname string, de *godirwalk.Dirent) error {
			// Ignore directories since we're only looking for files
			if !de.IsDir() {
				// Increment our index progress bar so we know the program is working and we know how far along we are
				indexBar.Add(1)
				// Submit a function to our workgroup that we'll execute later
				wp.Submit(func() {
					// I literally googled `go sha256 hash file` and clicked the first stackoverflow link

					// Get a hasher
					h := sha256.New()

					// Open the file
					f, err := os.Open(osPathname)
					if err != nil {
						log.Panic(err)
					}

					// Defer closing of the file until the end of the function
					defer f.Close()

					// Copy file in to our hasher
					if _, err := io.Copy(h, f); err != nil {
						log.Fatal(err)
					}

					// Get file info
					finfo, err := f.Stat()
					if err != nil {
						log.Fatal(err)
					}

					// Write the data we collected to the log file.
					// This will append to our log file something like...
					// C:\code\goindex\main.go, 23f3fa53025c860edf6f8e7d81b74973b4000dba388f74a5b93d52dafdc8077e, 2021-04-27 22:33:47.982338 +0000 UTC
					writeToFile(fmt.Sprintf("%s, %s, %s\n", osPathname, hex.EncodeToString(h.Sum(nil)), finfo.ModTime().UTC().String()), handle)

					// Increment the hashing progress bar
					hashBar.Add(1)
				})
			}
			return nil
		},
		// Callback for any errors we recieve when we're indexing, you could log these to a different file you if you wanted to
		ErrorCallback: func(osPathname string, err error) godirwalk.ErrorAction {
			fmt.Fprintf(os.Stderr, "ERROR: %s\n", err)
			return godirwalk.SkipNode
		},
		Unsorted: true,
	})

	// Check to see if the walk function itself returned any errors
	if err != nil {
		panic(err)
	}

	// Now that we know how many functions we have queued to run we can
	// initialize our hashing progress bar with the amount waiting in the queue
	hashBar = progressbar.Default(int64(wp.WaitingQueueSize()))

	// Cancel our context which will cause our workerpool to start working
	cancel()

	// Stop our workerpool and wait for all queued functions to complete
	wp.StopWait()
}
