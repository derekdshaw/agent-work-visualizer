package progress

import (
	"log"
	"time"
)

type Operation struct {
	name    string
	started time.Time
	stop    chan struct{}
	done    chan struct{}
}

func Begin(name string) *Operation {
	started := time.Now()
	log.Printf("%s", name)
	operation := &Operation{
		name:    name,
		started: started,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		defer close(operation.done)
		for {
			select {
			case <-ticker.C:
				log.Printf("%s still running (%s elapsed)", name, time.Since(started).Round(time.Second))
			case <-operation.stop:
				return
			}
		}
	}()
	return operation
}

func (operation *Operation) Finish(err error) {
	close(operation.stop)
	<-operation.done
	status := "complete"
	if err != nil {
		status = "failed"
	}
	log.Printf("%s %s (%s)", operation.name, status, time.Since(operation.started).Round(time.Millisecond))
}
