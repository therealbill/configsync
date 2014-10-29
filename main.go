package main

import (
	client "github.com/therealbill/libredis/client"
	"github.com/kelseyhightower/envconfig"
	"log"
	"os"
	"strings"
	"io"
	"fmt"
	"bufio"
	"strconv"
)

type LaunchConfig struct {
	SentinelConfigFile string
}


// SentinelPodConfig is a struct carrying information about a Pod's config as
// pulled from the sentinel config file.
type SentinelPodConfig struct {
    IP        string
    Port      int
    Quorum    int
    Name      string
    AuthToken string
    Sentinels map[string]string
}

// LocalSentinelConfig is a struct holding information about the sentinel we are 
// running on.
type LocalSentinelConfig struct {
    Name              string
    Host              string
    Port              int
    ManagedPodConfigs map[string]SentinelPodConfig
    Dir               string
}


var config LaunchConfig
var sconfig LocalSentinelConfig

// syncableDirectives is the list of directives to sync
// ideally this should also be controllable per invocation
var syncableDirectives []string


func init() {
	syncableDirectives = []string{ "hash-max-ziplist-entries",
	"hash-max-ziplist-value",
	"list-max-ziplist-entries",
	"list-max-ziplist-value",
	"zset-max-ziplist-entries",
	"zset-max-ziplist-value",
	"save",
	"appendfsync",
	"appendonly",
	"no-appendfsync-on-rewrite",
	"auto-aof-rewrite-percentage",
	"auto-aof-rewrite-min-size",
	"aof-rewrite-incremental-fsync",
	}

    err := envconfig.Process("configsync", &config)
    if err != nil {
        log.Fatal(err)
    }
	sconfig.ManagedPodConfigs = make(map[string]SentinelPodConfig)
	if config.SentinelConfigFile == "" { 
		config.SentinelConfigFile = "/etc/redis/sentinel.conf"
	}
}

// extractSentinelDirective parses the sentinel directives from the
// sentinel config file
func extractSentinelDirective(entries []string) error {
    switch entries[0] {
    case "monitor":
        pname := entries[1]
        port, _ := strconv.Atoi(entries[3])
        quorum, _ := strconv.Atoi(entries[4])
        spc := SentinelPodConfig{Name: pname, IP: entries[2], Port: port, Quorum: quorum}
        spc.Sentinels = make(map[string]string)
        addr := fmt.Sprintf("%s:%d", entries[2], port)
        _, exists := sconfig.ManagedPodConfigs[addr]
        if !exists {
            sconfig.ManagedPodConfigs[entries[1]] = spc
        }
        return nil

    case "auth-pass":
        pname := entries[1]
        pc := sconfig.ManagedPodConfigs[pname]
        pc.AuthToken = entries[2]
        sconfig.ManagedPodConfigs[pname] = pc
        return nil

    case "config-epoch", "leader-epoch", "current-epoch", "down-after-milliseconds", "known-sentinel", "known-slave":
        // We don't use these keys
        return nil

    default:
        err := fmt.Errorf("Unhandled sentinel directive: %+v", entries)
        log.Print(err)
        return nil
    }
    return nil
}



// LoadSentinelConfigFile loads the local config file pulled from the
// environment variable "CONFIGSYNC_SENTINELCONFIGFILE"
func  LoadSentinelConfigFile() error {
    file, err := os.Open(config.SentinelConfigFile)
    defer file.Close()
    if err != nil {
        log.Print(err)
        return err
    }
    bf := bufio.NewReader(file)
    for {
        rawline, err := bf.ReadString('\n')
        if err == nil || err == io.EOF {
            line := strings.TrimSpace(rawline)
            // ignore comments
            if strings.Contains(line, "#") {
                continue
            }
            entries := strings.Split(line, " ")
            //Most values are key/value pairs
            switch entries[0] {
            case "sentinel": // Have a sentinel directive
                err := extractSentinelDirective(entries[1:])
                if err != nil {
                    // TODO: Fix this to return a different error if we can't
                    // connect to the sentinel
                    log.Printf("Misshapen sentinel directive: '%s'", line, err)
                }
            case "port":
                iport, _ := strconv.Atoi(entries[1])
                sconfig.Port = iport
                //log.Printf("Local sentinel is bound to port %d", sconfig.Port)
            case "dir":
                sconfig.Dir = entries[1]
            case "bind":
               sconfig.Host = entries[1]
            case "":
                if err == io.EOF {
                    return nil
                }
            default:
                log.Printf("UNhandled Sentinel Directive: %s", line)
            }
        } else {
            log.Print("=============== LOAD FILE ERROR ===============")
            log.Fatal(err)
        }
    }
}



func synchronizeConfigs(pc SentinelPodConfig) error {
	address := fmt.Sprintf("%s:%d", pc.IP, pc.Port)
	master,err := client.DialWithConfig(&client.DialConfig{Address: address, Password: pc.AuthToken})
	if err != nil { return err }
	info, err := master.Info()
	if err != nil { return err }
	if info.Replication.Role != "master" {
		err := fmt.Errorf("Listed master does not have role 'master'. Aborting for safety")
		return err
	}
	directivesToSync := make(map[string]string)
	for _,d := range syncableDirectives {
		cv, _ := master.ConfigGet(d)
		directivesToSync[d] = cv[d]
	}
	for _,s := range info.Replication.Slaves {
		sadd := fmt.Sprintf("%s:%d", s.IP, s.Port)
		log.Printf("Sync: %s => %s", address, sadd)
		slave,err := client.DialWithConfig(&client.DialConfig{Address: sadd, Password: pc.AuthToken})
		if err != nil { log.Print("Unable to connecte to slave: ",err) }
		for k,v := range directivesToSync {
			err := slave.ConfigSet(k,v)
			if err != nil { log.Print("Err on config set: ", err) }
		}
	}
	return nil
}


func main() {
	LoadSentinelConfigFile()
	for _,pod := range sconfig.ManagedPodConfigs {
		err := synchronizeConfigs(pod)
		if err != nil {
			log.Printf("Error synchronizing configs for pod '%s'. Error='%s'", pod.Name, err)
		} else {
			log.Printf("Synchronized config for '%s'", pod.Name)
		}
	}
}
