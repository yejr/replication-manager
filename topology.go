package main

import (
	"errors"
	"fmt"
	"log"

	"github.com/go-sql-driver/mysql"
	"github.com/tanji/mariadb-tools/dbhelper"
)

// Start of topology detection
// Create a connection to each host and build list of slaves.
func topologyInit() error {
	servers = make([]*ServerMonitor, len(hostList))
	for k, url := range hostList {
		var err error
		servers[k], err = newServerMonitor(url)
		if verbose {
			log.Printf("DEBUG: Creating new server: %v", servers[k].URL)
		}
		if err != nil {
			if driverErr, ok := err.(*mysql.MySQLError); ok {
				if driverErr.Number == 1045 {
					return fmt.Errorf("ERROR: Database access denied: %s", err.Error())
				}
			}
			if verbose {
				log.Println("ERROR:", err)
			}
			log.Printf("INFO : Server %s is dead.", servers[k].URL)
			servers[k].State = stateFailed
			continue
		}
		defer servers[k].Conn.Close()
		if verbose {
			log.Printf("DEBUG: Checking if server %s is slave", servers[k].URL)
		}

		servers[k].refresh()
		if servers[k].UsingGtid != "" {
			if verbose {
				log.Printf("DEBUG: Server %s is configured as a slave", servers[k].URL)
			}
			servers[k].State = stateSlave
			slaves = append(slaves, servers[k])
		} else {
			if verbose {
				log.Printf("DEBUG: Server %s is not a slave. Setting aside", servers[k].URL)
			}
			servers[k].State = stateUnconn
		}
	}

	// If no slaves are detected, then bail out
	if len(slaves) == 0 {
		return errors.New("ERROR: No slaves were detected")
	}

	// Check that all slave servers have the same master.
	for _, sl := range slaves {
		if sl.hasSiblings(slaves) == false {
			return errors.New("ERROR: Multi-master topologies are not yet supported")
		}
	}

	// Depending if we are doing a failover or a switchover, we will find the master in the list of
	// dead hosts or unconnected hosts.
	if switchover != "" || failover == "monitor" {
		// First of all, get a server id from the slaves slice, they should be all the same
		sid := slaves[0].MasterServerID
		for k, s := range servers {
			if s.State == stateUnconn {
				if s.ServerID == sid {
					master = servers[k]
					master.State = stateMaster
					if verbose {
						log.Printf("DEBUG: Server %s was autodetected as a master", s.URL)
					}
					break
				}
			}
		}
	} else {
		// Slave master_host variable must point to dead master
		smh := slaves[0].MasterHost
		for k, s := range servers {
			if s.State == stateFailed {
				if s.Host == smh || s.IP == smh {
					master = servers[k]
					master.State = stateMaster
					if verbose {
						log.Printf("DEBUG: Server %s was autodetected as a master", s.URL)
					}
					break
				}
			}
		}
	}
	// Final check if master has been found
	if master == nil {
		if switchover != "" || failover == "monitor" {
			return errors.New("ERROR: Could not autodetect a master")
		}
		return errors.New("ERROR: Could not autodetect a failed master")
	}
	// End of autodetection code

	for _, sl := range slaves {
		if verbose {
			log.Printf("DEBUG: Checking if server %s is a slave of server %s", sl.Host, master.Host)
		}
		if dbhelper.IsSlaveof(sl.Conn, sl.Host, master.IP) == false {
			log.Printf("WARN : Server %s is not a slave of declared master %s", master.URL, master.Host)
		}
	}
	return nil
}
