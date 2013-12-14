/*Blacksheepwall is a hostname reconnaissance tool, it is similar to other
tools, but has a focus on speed.*/

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/tomsteele/blacksheepwall/bsw"
	"log"
	"net"
	"os"
	"sort"
	"text/tabwriter"
)

const usage = `
  Usage: blacksheepwall [options] <ip address or CIDR>

  Options:
  -h, --help            Show Usage and exit.
  -version              Show version and exit.
  -debug                Enable debugging and show errors returned from tasks.
  -concurrency <int>    Max amount of concurrent tasks.    [default: 100]
  -server <string>      DNS server address.    [default: "8.8.8.8"]
  -input <string>       Line separated file of networks (CIDR) or 
                        IP Addresses.
  -ipv6	                Look for additional AAAA records where applicable.
  -domain <string>      Target domain to use for certain tasks.
  -dictionary <string>  Attempt to retrieve the CNAME and A record for
                        each subdomain in the line separated file.
  -yandex <string>      Provided a Yandex search XML API url. Use the Yandex 
                        search 'rhost:' operator to find subdomains of a 
                        provided domain..
  -bing	<string>        Provided a base64 encoded API key. Use the Bing search
                        API's 'ip:' operator to lookup hostnames for each host.
  -headers              Perform HTTP(s) requests to each host and look for 
                        hostnames in a possible Location header.
  -reverse              Retrieve the PTR for each host.
  -tls                  Attempt to retrieve names from TLS certificates 
                        (CommonName and Subject Alternative Name).
  -viewdns              Lookup each host using viewdns.info's Reverse IP
                        Lookup function.
  -srv 					Find DNS SRV record and retrieve associated hostname/IP info.
  -fcrdns               Verify results by attempting to retrieve the A or AAAA record for
                        each result previously identified hostname.
  -clean                Print results as unique hostnames for each host.
  -csv                  Print results in csv format.
  -json                 Print results as JSON.

`

// Processes a list of IP addresses or networks in CIDR format.
// Returning a list of all possible IP addresses.
func linesToIpList(lines []string) ([]string, error) {
	ipList := []string{}
	for _, line := range lines {
		if net.ParseIP(line) != nil {
			ipList = append(ipList, line)
		} else if ip, network, err := net.ParseCIDR(line); err == nil {
			for ip := ip.Mask(network.Mask); network.Contains(ip); increaseIp(ip) {
				ipList = append(ipList, ip.String())
			}
		} else {
			return ipList, errors.New("\"" + line + "\" is not an IP Address or CIDR Network")
		}
	}
	return ipList, nil
}

// Increases an IP by a single address.
func increaseIp(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

// Reads lines from a file and return as a slice.
func readFileLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	lines := []string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

type task func() (bsw.Results, error)
type empty struct{}

func main() {
	// Command line options. For usage information see the
	// usage variable above.
	var (
		flVersion     = flag.Bool("version", false, "")
		flConcurrency = flag.Int("concurrency", 100, "")
		flDebug       = flag.Bool("debug", false, "")
		flipv6        = flag.Bool("ipv6", false, "")
		flServerAddr  = flag.String("server", "8.8.8.8", "")
		flIpFile      = flag.String("input", "", "")
		flReverse     = flag.Bool("reverse", false, "")
		flHeader      = flag.Bool("headers", false, "")
		flTLS         = flag.Bool("tls", false, "")
		flViewDnsInfo = flag.Bool("viewdns", false, "")
		flSRV         = flag.Bool("srv", false, "")
		flBing        = flag.String("bing", "", "")
		flYandex      = flag.String("yandex", "", "")
		flDomain      = flag.String("domain", "", "")
		flDictFile    = flag.String("dictionary", "", "")
		flFcrdns      = flag.Bool("fcrdns", false, "")
		flClean       = flag.Bool("clean", false, "")
		flCsv         = flag.Bool("csv", false, "")
		flJson        = flag.Bool("json", false, "")
	)
	flag.Usage = func() { fmt.Print(usage) }
	flag.Parse()

	if *flVersion {
		fmt.Println("blacksheepwall version ", bsw.VERSION)
		os.Exit(0)
	}

	// Holds all IP addresses for testing.
	ipAddrList := []string{}

	// Used to hold a ip or CIDR range passed as fl.Arg(0).
	var flNetwork string

	// Verify that some sort of work load was given in commands.
	if *flIpFile == "" && *flDomain == "" && len(flag.Args()) < 1 {
		log.Fatal("You didn't provide any work for me to do")
	}
	if *flYandex != "" && *flDomain == "" {
		log.Fatal("Yandex API requires domain set with -domain")
	}
	if *flDictFile != "" && *flDomain == "" {
		log.Fatal("Dictionary lookup requires domain set with -domain")
	}
	if *flDomain == "" && *flSRV == true {
		log.Fatal("SRV lookup requires domain set with -domain")
	}
	if *flDomain != "" && *flYandex == "" && *flDictFile == "" && *flSRV == false {
		log.Fatal("-domain provided but no methods provided that use it")
	}

	// Get first argument that is not an option and turn it into a list of IPs.
	if len(flag.Args()) > 0 {
		flNetwork = flag.Arg(0)
		list, err := linesToIpList([]string{flNetwork})
		if err != nil {
			log.Fatal(err.Error())
		}
		ipAddrList = append(ipAddrList, list...)
	}

	// If file given as -input, read lines and turn each possible IP or network into
	// a list of IPs. Appends list to ipAddrList. Will fail fatally if line in file
	// is not a valid IP or CIDR range.
	if *flIpFile != "" {
		lines, err := readFileLines(*flIpFile)
		if err != nil {
			log.Fatal("Error reading " + *flIpFile + " " + err.Error())
		}
		list, err := linesToIpList(lines)
		if err != nil {
			log.Fatal(err.Error())
		}
		ipAddrList = append(ipAddrList, list...)
	}

	// tracker: Chanel uses an empty struct to track when all goroutines in the pool
	//          have completed as well as a single call from the gatherer.
	//
	// tasks:   Chanel used in the goroutine pool to manage incoming work. A task is
	//          a function wrapper that returns a slice of results and a possible error.
	//
	// res:     When each task is called in the pool, it will send valid results to
	//          the res channel.
	tracker := make(chan empty)
	tasks := make(chan task, *flConcurrency)
	res := make(chan bsw.Results, *flConcurrency)
	// Use a map that acts like a set to store only unique results.
	resMap := make(map[bsw.Result]bool)

	// Start up *flConcurrency amount of goroutines.
	log.Printf("Spreading tasks across %d goroutines", *flConcurrency)
	for i := 0; i < *flConcurrency; i++ {
		go func() {
			var c = 0
			for def := range tasks {
				result, err := def()
				if m := c % 2; m == 0 {
					c = 3
					os.Stderr.WriteString("\rWorking \\")
				} else {
					c = 2
					os.Stderr.WriteString("\rWorking /")
				}
				if err != nil && *flDebug {
					log.Println(err.Error())
				}
				if err == nil {
					res <- result
				}
			}
			tracker <- empty{}
		}()
	}

	// Ingest incoming results.
	go func() {
		for result := range res {
			if len(result) < 1 {
				continue
			}
			if *flFcrdns {
				for _, r := range result {
					ip, err := bsw.LookupName(r.Hostname, *flServerAddr)
					if err == nil && len(ip) > 0 {
						resMap[bsw.Result{Source: "fcrdns", IP: ip, Hostname: r.Hostname}] = true
					}
					ip, err = bsw.LookupName6(r.Hostname, *flServerAddr)
					if err == nil && len(ip) > 0 {
						resMap[bsw.Result{Source: "fcrdns", IP: ip, Hostname: r.Hostname}] = true
					}
				}
			} else {
				for _, r := range result {
					resMap[r] = true
				}
			}
		}
		tracker <- empty{}
	}()

	// Bing has two possible search paths. We need to find which one is valid.
	var bingPath string
	if *flBing != "" {
		p, err := bsw.FindBingSearchPath(*flBing)
		if err != nil {
			log.Fatal(err.Error())
		}
		bingPath = p
	}

	// IP based functionality should be added to the pool here.
	for _, h := range ipAddrList {
		host := h
		if *flReverse {
			tasks <- func() (bsw.Results, error) { return bsw.Reverse(host, *flServerAddr) }
		}
		if *flTLS {
			tasks <- func() (bsw.Results, error) { return bsw.TLS(host) }
		}
		if *flViewDnsInfo {
			tasks <- func() (bsw.Results, error) { return bsw.ViewDnsInfo(host) }
		}
		if *flBing != "" && bingPath != "" {
			tasks <- func() (bsw.Results, error) { return bsw.BingAPI(host, *flBing, bingPath) }
		}
		if *flHeader {
			tasks <- func() (bsw.Results, error) { return bsw.Headers(host) }
		}
	}

	// Domain based functions will likely require separate blocks and should be added below.

	// Subdomain dictionary guessing.
	if *flDictFile != "" && *flDomain != "" {
		nameList, err := readFileLines(*flDictFile)
		if err != nil {
			log.Fatal("Error reading " + *flDictFile + " " + err.Error())
		}
		// Get an IP for a possible wildcard domain and use it as a blacklist.
		blacklist := bsw.GetWildCard(*flDomain, *flServerAddr)
		var blacklist6 string
		if *flipv6 {
			blacklist6 = bsw.GetWildCard6(*flDomain, *flServerAddr)
		}
		for _, n := range nameList {
			sub := n
			tasks <- func() (bsw.Results, error) { return bsw.Dictionary(*flDomain, sub, blacklist, *flServerAddr) }
			if *flipv6 {
				tasks <- func() (bsw.Results, error) { return bsw.Dictionary6(*flDomain, sub, blacklist6, *flServerAddr) }
			}
		}
	}

	if *flSRV != false && *flDomain != "" {
		tasks <- func() (bsw.Results, error) { return bsw.LookupSRV(*flDomain, *flServerAddr) }
	}

	if *flYandex != "" && *flDomain != "" {
		tasks <- func() (bsw.Results, error) { return bsw.YandexAPI(*flDomain, *flYandex, *flServerAddr) }
	}

	// Close the tasks channel after all jobs have completed and for each
	// goroutine in the pool receive an empty message from  tracker.
	close(tasks)
	for i := 0; i < *flConcurrency; i++ {
		<-tracker
	}
	close(res)
	// Receive and empty message from the result gatherer.
	<-tracker
	os.Stderr.WriteString("\r")
	log.Println("All tasks completed\n")

	// Create a results slice from the unique set in resMap. Allows for sorting.
	results := bsw.Results{}
	for k, _ := range resMap {
		results = append(results, k)
	}
	sort.Sort(results)

	switch {
	case *flJson:
		j, _ := json.MarshalIndent(results, "", "    ")
		fmt.Println(string(j))
	case *flCsv:
		for _, r := range results {
			fmt.Printf("%s,%s,%s\n", r.Hostname, r.IP, r.Source)
		}
	case *flClean:
		cleanSet := make(map[string][]string)
		for _, r := range results {
			cleanSet[r.IP] = append(cleanSet[r.IP], r.Hostname)
		}
		for k, v := range cleanSet {
			fmt.Printf("%s:\n", k)
			for _, h := range v {
				fmt.Printf("\t%s\n", h)
			}
		}
	default:
		w := tabwriter.NewWriter(os.Stdout, 0, 8, 4, ' ', 0)
		fmt.Fprintln(w, "IP\tHostname\tSource")
		for _, r := range results {
			fmt.Fprintf(w, "%s\t%s\t%s\n", r.IP, r.Hostname, r.Source)
		}
		w.Flush()
	}
}