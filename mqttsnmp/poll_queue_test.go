package mqttsnmp

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
		ch[i] = newEmptyChannelConfig()
		ch[i].Name = strconv.Itoa(i)
	}

	// create slice of dummy channels
	ar := make([]pollQuery, 10)
	for i := range ar {
		ar[i] = pollQuery{
			channel:  ch[i],
			deadline: time.Date(2016, time.November, 1, 0, 0, i, 0, time.UTC),
		}
	}

	// create subqueue itself
	q := newPollQueue(ar)

	t := time.Date(2016, time.November, 1, 0, 0, 5, 0, time.UTC)

	// check elements
	for i := 0; i < 10; i++ {
		// check pending check
		p.Equal(q.isTopPending(t), i <= 5)

		elem, err := q.pop()
		p.NoError(err, "failed to get elem from queue")
		p.Equal(elem.channel.Name, strconv.Itoa(i))
	}

}

func (p *PollQueueTest) TestPollTable() {
	// create 3 test subqueues
	ch := make([]*ChannelConfig, 15)
	for i := range ch {
		ch[i] = newEmptyChannelConfig()
		ch[i].Name = strconv.Itoa(i)
	}

	ar := make([]pollQuery, len(ch))
	for i := range ar {
		ar[i] = pollQuery{
			channel:  ch[i],
			deadline: time.Date(2016, time.November, 1, 0, 0, 0, 0, time.UTC),
		}
	}

	// subqueues
	q1 := newPollQueue(ar[0:5])
	q2 := newPollQueue(ar[5:10])
	q3 := newPollQueue(ar[10:15])

	// deadline
	t := time.Date(2016, time.November, 1, 0, 0, 3, 0, time.UTC)

	// create poll table
	pt := newPollTable()
	pt.addQueue(q1, 100)
	pt.addQueue(q2, 300)
	pt.addQueue(q3, 500)

	// check next poll time
	nextPoll, err := pt.nextPollTime()
	p.NoError(err, "failed to get next poll time")
	p.Equal(nextPoll, ar[0].deadline)

	// test first poll
	c := make(chan pollQuery, 15)

	num := pt.poll(c, t)
	for i := 0; i < num; i++ {
		query := <-c
		p.Equal(query, ar[i])
		p.NotEqual(i, 16)
	}

	// check next poll time again
	nextPoll, err = pt.nextPollTime()
	p.NoError(err, "failed to get next poll time")
	p.Equal(nextPoll, t.Add(100*time.Millisecond))

	// shift time for 160 ms, poll again
	for i := 0; i < 5; i++ {
		ar[i].deadline = t.Add(100 * time.Millisecond)
	}
	t2 := t.Add(160 * time.Millisecond)

	num = pt.poll(c, t2)
	for i := 0; i < num; i++ {
		query := <-c
		p.Equal(query, ar[i])
		p.NotEqual(i, 6)
	}

	// shift time for 160 ms again, poll
	for i := 0; i < 5; i++ {
		ar[i].deadline = t2.Add(100 * time.Millisecond)
	}
	for i := 5; i < 10; i++ {
		ar[i].deadline = t.Add(300 * time.Millisecond)
	}
	t3 := t2.Add(160 * time.Millisecond)

	num = pt.poll(c, t3)
	for i := 0; i < num; i++ {
		query := <-c
		p.Equal(query, ar[i])
		p.NotEqual(i, 11)
	}
}

func TestPollQueue(t *testing.T) {
	s := new(PollQueueTest)

	s.SetupTestFixture(t)
	defer s.TearDownTestFixture(t)

	testutils.RunSuites(t, s)
}
