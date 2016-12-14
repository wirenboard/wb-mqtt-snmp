package mqtt_snmp

import (
	"fmt"
	"sort"
	"time"
)

// Poll query unit
// Contains pointer to SNMP connection, OID to poll, channel
// to send result to and deadline time
// Query is put into a queue according to its poll interval

type PollQuery struct {
	Channel  *ChannelConfig
	Deadline time.Time
}

// Poll result is data sent from PollWorker to PublishWorker
// Data is processed by Conv function by PollWorker
type PollResult struct {
	Channel *ChannelConfig
	Data    string
}

type PollError struct {
	Channel *ChannelConfig
	Error   string
}

// Poll queue structure
// Just a ring buffer full of queries
type PollQueue struct {
	size   int
	start  int
	end    int
	empty  bool
	buffer []PollQuery
}

// Create an empty poll queue with size specified
func NewEmptyPollQueue(size int) *PollQueue {
	return &PollQueue{
		size:   size,
		start:  0,
		end:    0,
		empty:  true,
		buffer: make([]PollQuery, size),
	}
}

// Create a poll queue from slice
func NewPollQueue(queries []PollQuery) *PollQueue {
	q := NewEmptyPollQueue(len(queries))

	for i := range queries {
		q.Push(queries[i])
	}

	return q
}

// Push new query to the end of the queue
func (p *PollQueue) Push(q PollQuery) error {

	// drop error on overflow
	if p.start == p.end && !p.empty {
		return fmt.Errorf("poll queue overflow")
	}

	p.empty = false

	p.buffer[p.end] = q

	p.end += 1
	if p.end == p.size {
		p.end = 0
	}

	return nil
}

// Pop a queue from the head of the queue
func (p *PollQueue) Pop() (q PollQuery, err error) {
	err = nil

	if p.start == p.end && p.empty {
		err = fmt.Errorf("poll queue is empty")
		return
	}

	q = p.buffer[p.start]

	p.start += 1
	if p.start == p.size {
		p.start = 0
	}

	if p.start == p.end {
		p.empty = true
	}

	return
}

// Check if queue on the top is pending
func (p *PollQueue) IsTopPending(currentTime time.Time) bool {
	return !p.empty && (p.buffer[p.start].Deadline.Before(currentTime) || p.buffer[p.start].Deadline.Equal(currentTime))
}

// Is queue empty
func (p *PollQueue) IsEmpty() bool {
	return p.empty
}

// Get head element without removing it
func (p *PollQueue) GetHead() (q PollQuery, err error) {
	err = nil
	if p.IsEmpty() {
		err = fmt.Errorf("fail to get head: queue is empty")
		return
	}

	q = p.buffer[p.start]
	return
}

// Poll table is a set of poll queues with different
// poll_interval in each queue. This allows us to avoid
// sorting and might work well with lots of channels with
// equal poll intervals
type PollTable struct {
	// Map from poll_interval to specific queue
	Queues map[int]*PollQueue

	// List of possible poll_intervals (aka queues keys)
	// Sorted in ascending order (to process
	// more frequent polls first)
	Intervals []int
}

func NewPollTable() *PollTable {
	return &PollTable{
		Queues:    make(map[int]*PollQueue),
		Intervals: make([]int, 0),
	}
}

// Add queue to poll table
func (t *PollTable) AddQueue(q *PollQueue, interval int) error {
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

// Do "poll" action
// Push pending polls into a given channel and requeue them
// Returns number of polls sent into process
func (t *PollTable) Poll(out chan PollQuery, deadline time.Time) int {
	count := 0

	// process key by key
	for _, poll_interval := range t.Intervals {
		for t.Queues[poll_interval].IsTopPending(deadline) {
			head, err := t.Queues[poll_interval].Pop()
			if err != nil {
				// TODO: log error here
				return count
			}

			// fmt.Printf("[polltable] Send request from head: %v\n", head)
			out <- head
			head.Deadline = deadline.Add(time.Duration(poll_interval) * time.Millisecond)
			t.Queues[poll_interval].Push(head)
			count += 1
		}
	}

	return count
}

// Get next poll time point
func (t *PollTable) NextPollTime() (minTime time.Time, err error) {
	// Go through all queues heads and get minimal time
	var head, h PollQuery
	head, err = t.Queues[t.Intervals[0]].GetHead()
	if err != nil {
		return
	}

	minTime = head.Deadline
	for _, interval := range t.Intervals {
		h, err = t.Queues[interval].GetHead()
		if err != nil {
			return
		}
		d := h.Deadline
		if d.Before(minTime) {
			minTime = d
		}
	}

	return
}
