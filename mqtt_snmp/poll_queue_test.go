package mqtt_snmp

import (
	// "github.com/contactless/wbgo"
	"github.com/contactless/wbgo/testutils"
	// "log"
	"strconv"
	"testing"
	"time"
)

type PollQueueTest struct {
	testutils.Suite
}

func (p *PollQueueTest) SetupTestFixture(t *testing.T) {

}

func (p *PollQueueTest) TearDownTestFixture(t *testing.T) {

}

func (p *PollQueueTest) SetupTest() {

}

func (p *PollQueueTest) TearDownTest() {

}

func (p *PollQueueTest) TestSubQueue() {
	// create dummy channel configurations
	ch := make([]*ChannelConfig, 10)
	for i := range ch {
		ch[i] = NewEmptyChannelConfig()
		ch[i].Name = strconv.Itoa(i)
	}

	// create slice of dummy channels
	ar := make([]PollQuery, 10)
	for i := range ar {
		ar[i] = PollQuery{
			Channel:  ch[i],
			Deadline: time.Date(2016, time.November, 1, 0, 0, i, 0, time.UTC),
		}
	}

	// create subqueue itself
	q := NewPollQueue(ar)

	t := time.Date(2016, time.November, 1, 0, 0, 5, 0, time.UTC)

	// check elements
	for i := 0; i < 10; i++ {
		// check pending check
		p.Equal(q.IsTopPending(t), i < 5)

		elem, err := q.Pop()
		p.NoError(err, "failed to get elem from queue")
		p.Equal(elem.Channel.Name, strconv.Itoa(i))
	}

}

func (p *PollQueueTest) TestPollTable() {
	// create 3 test subqueues
	ch := make([]*ChannelConfig, 15)
	for i := range ch {
		ch[i] = NewEmptyChannelConfig()
		ch[i].Name = strconv.Itoa(i)
	}

	ar := make([]PollQuery, len(ch))
	for i := range ar {
		ar[i] = PollQuery{
			Channel:  ch[i],
			Deadline: time.Date(2016, time.November, 1, 0, 0, 0, 0, time.UTC),
		}
	}

	// subqueues
	q1 := NewPollQueue(ar[0:5])
	q2 := NewPollQueue(ar[5:10])
	q3 := NewPollQueue(ar[10:15])

	// deadline
	t := time.Date(2016, time.November, 1, 0, 0, 3, 0, time.UTC)

	// create poll table
	pt := NewPollTable()
	pt.AddQueue(q1, 100)
	pt.AddQueue(q2, 300)
	pt.AddQueue(q3, 500)

	// test first poll
	c := make(chan PollQuery, 15)
	pt.Poll(c, t)

	i := 0
	for query := range c {
		p.Equal(query, ar[i])
		i += 1
	}

	// shift time for 160 ms, poll again
	for i := 0; i < 5; i++ {
		ar[i].Deadline = t.Add(100 * time.Millisecond)
	}
	t2 := t.Add(160 * time.Millisecond)
	c = make(chan PollQuery, 15)
	pt.Poll(c, t2)
	i = 0
	for query := range c {
		p.Equal(query, ar[i])
		i += 1
		p.NotEqual(i, 6)
	}

	// shift time for 160 ms again, poll
	for i := 0; i < 5; i++ {
		ar[i].Deadline = t2.Add(100 * time.Millisecond)
	}
	for i := 5; i < 10; i++ {
		ar[i].Deadline = t.Add(300 * time.Millisecond)
	}
	t3 := t2.Add(160 * time.Millisecond)
	c = make(chan PollQuery, 15)
	pt.Poll(c, t3)
	i = 0
	for query := range c {
		p.Equal(query, ar[i])
		i += 1
		p.NotEqual(i, 11)
	}
}

func TestPollQueue(t *testing.T) {
	s := new(PollQueueTest)

	s.SetupTestFixture(t)
	defer s.TearDownTestFixture(t)

	testutils.RunSuites(t, s)
}
