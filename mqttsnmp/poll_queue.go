package mqttsnmp

import (
	"fmt"
	"sort"
	"time"
)

// pollQuery contains pointer to SNMP connection, OID to poll, channel
// to send result to and deadline time
// Query is put into a queue according to its poll interval
type pollQuery struct {
	channel  *ChannelConfig
	deadline time.Time
}

// pollResult is data sent from PollWorker to PublishWorker
// Data is processed by Conv function by PollWorker
type pollResult struct {
	channel *ChannelConfig
	data    interface{}
}

// pollError represents poll error
type pollError struct {
	channel *ChannelConfig
	err     string
}

// pollQueue is just a ring buffer full of queries
type pollQueue struct {
	size   int
	start  int
	end    int
	empty  bool
	buffer []pollQuery
}

// newEmptyPollQueue creates an empty poll queue with size specified
func newEmptyPollQueue(size int) *pollQueue {
	return &pollQueue{
		size:   size,
		start:  0,
		end:    0,
		empty:  true,
		buffer: make([]pollQuery, size),
	}
}

// newPollQueue creates a poll queue from slice
func newPollQueue(queries []pollQuery) *pollQueue {
	q := newEmptyPollQueue(len(queries))

	for i := range queries {
		q.push(queries[i])
	}

	return q
}

// push new query to the end of the queue
func (p *pollQueue) push(q pollQuery) error {

	// drop error on overflow
	if p.start == p.end && !p.empty {
		return fmt.Errorf("poll queue overflow")
	}

	p.empty = false

	p.buffer[p.end] = q

	p.end++
	if p.end == p.size {
		p.end = 0
	}

	return nil
}

// pop a queue from the head of the queue
func (p *pollQueue) pop() (q pollQuery, err error) {
	err = nil

	if p.start == p.end && p.empty {
		err = fmt.Errorf("poll queue is empty")
		return
	}

	q = p.buffer[p.start]

	p.start++
	if p.start == p.size {
		p.start = 0
	}

	if p.start == p.end {
		p.empty = true
	}

	return
}

// isTopPending checks if queue on the top is pending
func (p *pollQueue) isTopPending(currentTime time.Time) bool {
	return !p.empty && (p.buffer[p.start].deadline.Before(currentTime) || p.buffer[p.start].deadline.Equal(currentTime))
}

// isEmpty checks if queue is empty
func (p *pollQueue) isEmpty() bool {
	return p.empty
}

// getHead returns head element without removing it
func (p *pollQueue) getHead() (q pollQuery, err error) {
	err = nil
	if p.isEmpty() {
		err = fmt.Errorf("fail to get head: queue is empty")
		return
	}

	q = p.buffer[p.start]
	return
}

// pollTable is a set of poll queues with different
// poll_interval in each queue. This allows us to avoid
// sorting and might work well with lots of channels with
// equal poll intervals
type pollTable struct {
	// Map from poll_interval to specific queue
	Queues map[int]*pollQueue

	// List of possible poll_intervals (aka queues keys)
	// Sorted in ascending order (to process
	// more frequent polls first)
	Intervals []int
}

// newPollTable constructs new PollTable object
func newPollTable() *pollTable {
	return &pollTable{
		Queues:    make(map[int]*pollQueue),
		Intervals: make([]int, 0),
	}
}

// addQueue adds queue to poll table
func (t *pollTable) addQueue(q *pollQueue, interval int) error {
	// check if such queue is presented here
	if _, ok := t.Queues[interval]; ok {
		return fmt.Errorf("queue with poll interval %d is already here", interval)
	}

	// add queue in map
	t.Queues[interval] = q
	t.Intervals = append(t.Intervals, interval)

	// sort intervals
	sort.Ints(t.Intervals)

	return nil
}

// poll pushes pending polls into a given channel and requeue them
// Returns number of polls sent into process
func (t *pollTable) poll(out chan pollQuery, deadline time.Time) int {
	count := 0

	// process key by key
	for _, pollInterval := range t.Intervals {
		for t.Queues[pollInterval].isTopPending(deadline) {
			head, err := t.Queues[pollInterval].pop()
			if err != nil {
				// TODO: log error here
				return count
			}

			// fmt.Printf("[polltable] Send request from head: %v\n", head)
			out <- head
			head.deadline = deadline.Add(time.Duration(pollInterval) * time.Millisecond)
			t.Queues[pollInterval].push(head)
			count++
		}
	}

	return count
}

// nextPollTime returns next poll time point
func (t *pollTable) nextPollTime() (minTime time.Time, err error) {
	// Go through all queues heads and get minimal time
	var head, h pollQuery
	head, err = t.Queues[t.Intervals[0]].getHead()
	if err != nil {
		return
	}

	minTime = head.deadline
	for _, interval := range t.Intervals {
		h, err = t.Queues[interval].getHead()
		if err != nil {
			return
		}
		d := h.deadline
		if d.Before(minTime) {
			minTime = d
		}
	}

	return
}
