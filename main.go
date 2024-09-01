package main

import (
	// "golang.org/x/term"
	// "golang.org/x/crypto/ssh"
	"bufio"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	mu         sync.Mutex
	contentMap map[string][]line
)

type content struct {
	name    string
	content string
	stderr  bool
}

type line struct {
	content string
	stderr  bool
}

type HostInfo struct {
	addr string
}

var (
	cyan  *color.Color = color.New(color.FgCyan)
	red                = color.New(color.FgRed)
	green              = color.New(color.FgGreen)
)

var cmd = &cobra.Command{
	Use:       "sshp",
	Short:     "SSH Parallel",
	Example:   "sshp echo hello",
	ValidArgs: []string{"command"},
	Run: func(cmd *cobra.Command, args []string) {
		maxParallel, err := cmd.Flags().GetInt8("max-parallel")
		if err != nil {
			log.Fatal("failed to parse arguments")
		}

		isGrouped, err := cmd.Flags().GetBool("join")
		if err != nil {
			log.Fatal("failed to parse arguments")
		}

		filename, err := cmd.Flags().GetString("file")
		if err != nil {
			log.Fatal("failed to parse arguments")
		}
		if filename == "" {
			userHost, err := os.UserHomeDir()
			if err != nil {
				log.Fatal("failed to locate hosts file")
			}
			filename = filepath.Join(userHost, "hosts")
		}

		userCmd := strings.Join(args, " ")
		if userCmd == "" {
			log.Fatal("failed to read command")
		}

		writeChannel := make(chan content, maxParallel*2)
		defer close(writeChannel)

		var wg sync.WaitGroup
		if !isGrouped {
			wg.Add(1)
			go func() {
				for outPrint := range writeChannel {
					mu.Lock()
					green.Printf("[%s]: ", outPrint.name)
					if outPrint.stderr {
						red.Println(outPrint.content)
					} else {
						fmt.Println(outPrint.content)
					}
					mu.Unlock()
				}
				wg.Done()
			}()
		}

		hosts := parser(filename)
		/*
		 * TODO: SSH Logic
		 */

		if !isGrouped {
			wg.Wait()
			return
		}

		for _, h := range hosts {
			v, ok := contentMap[h.addr]
			green.Printf("[%s]:\n", h.addr)
			if !ok {
				red.Println("<sshp: Failed to read output of the command>")
				continue
			}
			for _, l := range v {
				if l.stderr {
					red.Println(l.content)
				} else {
					fmt.Println(l.content)
				}
			}
		}
	},
}

func parser(filepath string) []HostInfo {
	file, err := os.Open(filepath)
	if err != nil {
		log.Fatal("failed to open host file")
	}

	hosts := make([]HostInfo, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		l := strings.Trim(scanner.Text(), " \n\t")
		if len(l) == 0 || string(l[0]) == "#" {
			continue
		}

		host := HostInfo{l}
		hosts = append(hosts, host)
	}

	if len(hosts) == 0 {
		log.Fatal("failed to read host file.")
	}
	return hosts
}

func askForConfirmation(s string) bool {
	reader := bufio.NewReader(os.Stdin)
	for {
		cyan.Printf("\n%s [y/n]: ", s)

		response, err := reader.ReadString('\n')
		if err != nil {
			log.Fatal(err)
		}
		response = strings.ToLower(strings.TrimSpace(response))
		if response == "y" || response == "yes" {
			return true
		} else if response == "n" || response == "no" {
			return false
		}
	}
}

func main() {
	cmd.Flags().Int8P("max-parallel", "m", 16, "The maximum allowed number for parallel ssh sessions")
	cmd.Flags().StringP("file", "f", "", "the file containing host mechine")
	cmd.Flags().BoolP("join", "j", false, "weither to print each line as it comes or group per host based")

	done := make(chan struct{})

	go func() {
		if err := cmd.Execute(); err != nil {
			log.Fatal(err)
		}
		done <- struct{}{}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

exitProg:
	for {
		select {
		case <-sigs:
			mu.Lock()
			if ok := askForConfirmation("Are you sure you want to terminate the program:"); ok {
				break exitProg
			}
			mu.Unlock()
		case <-done:
			cyan.Println("Done!")
			break exitProg
		}
	}
}
