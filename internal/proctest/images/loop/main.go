// Command loop is the test processor that never finishes: it sleeps until
// it is killed.
package main

import "time"

func main() {
	for {
		time.Sleep(time.Hour)
	}
}
