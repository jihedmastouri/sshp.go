package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	ssh "golang.org/x/crypto/ssh"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	mu         sync.Mutex
	contentMap sync.Map
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
	addr     string
	password string
	username string
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

		isSilent, err := cmd.Flags().GetBool("silent")
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

		var writerWg sync.WaitGroup

		if !isGrouped || !isSilent {
			writerWg.Add(1)
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
				writerWg.Done()
			}()
		}

		hosts := parser(filename)
		hostChannel := make(chan HostInfo, 1)

		go func() {
			for _, h := range hosts {
				hostChannel <- h
			}
		}()

		var sshWg sync.WaitGroup
		for range min(maxParallel, int8(len(hosts))) {
			sshWg.Add(1)
			go func() {
				host := <-hostChannel

				config := ssh.ClientConfig{
					User: host.username,
					Auth: []ssh.AuthMethod{
						ssh.Password(host.password),
					},
					HostKeyCallback: ssh.InsecureIgnoreHostKey(),
				}

				client, err := ssh.Dial("tcp", host.addr, &config)
				if err != nil {
					log.Println(err)
					sshWg.Done()
					return
				}
				defer client.Close()

				session, err := client.NewSession()
				if err != nil {
					log.Fatal("Failed to create ssh session: ", err)
				}
				defer session.Close()

				var readWg sync.WaitGroup
				if !isSilent {
					readWg.Add(2)

					stdErrPipe, err := session.StderrPipe()
					if err != nil {
						log.Println(err)
					}
					go readPipe(stdErrPipe, host, isGrouped, writeChannel, &readWg, true)

					stdOutPipe, err := session.StdoutPipe()
					if err != nil {
						log.Println(err)
					}
					go readPipe(stdOutPipe, host, isGrouped, writeChannel, &readWg, false)
				}

				session.Run(userCmd)

				readWg.Wait()
				sshWg.Done()
			}()
		}

		sshWg.Wait()
		close(writeChannel)

		if !isGrouped || !isSilent {
			writerWg.Wait()
			return
		}

		if isSilent {
			return
		}

		for _, h := range hosts {
			mu.Lock()
			v, ok := contentMap.Load(h.addr)
			green.Printf("[%s]:\n", h.addr)
			if !ok {
				red.Println("<sshp: Failed to read output of the command>")
				continue
			}
			for _, l := range v.([]line) {
				if l.stderr {
					red.Println(l.content)
				} else {
					fmt.Println(l.content)
				}
			}
			mu.Unlock()
		}
	},
}

func readPipe(pipe io.Reader, host HostInfo, isGrouped bool, writeChannel chan<- content, wg *sync.WaitGroup, isErr bool) {
	lines := []line{}

	scanner := bufio.NewScanner(pipe)
	for scanner.Scan() {
		if isGrouped {
			lines = append(lines, line{
				stderr:  isErr,
				content: scanner.Text(),
			})
		} else {
			writeChannel <- content{
				name:    host.addr,
				content: scanner.Text(),
				stderr:  isErr,
			}
		}
	}

	prevLines, exists := contentMap.Load(host.addr)
	if exists {
		contentMap.Store(
			host.addr,
			append(prevLines.([]line), lines...),
		)
	} else {
		contentMap.Store(host.addr, lines)
	}

	wg.Done()
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

		infos := strings.Split(l, " ")
		if len(infos) != 3 {
			continue
		}
		host := HostInfo{infos[0], infos[1], infos[2]}
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
	cmd.Flags().StringP("file", "f", "", "The file containing host mechine")
	cmd.Flags().BoolP("join", "j", false, "Weither to print each line as it comes or group per host based")
	cmd.Flags().BoolP("silent", "s", false, "Work in slience. No Output will be shown.")

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
