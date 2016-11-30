package mqtt_snmp

import (
	// "github.com/contactless/wbgo"
	"github.com/contactless/wbgo/testutils"
	"log"
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

	log.Printf("%+v", q)

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

func TestPollQueue(t *testing.T) {
	s := new(PollQueueTest)

	s.SetupTestFixture(t)
	defer s.TearDownTestFixture(t)

	testutils.RunSuites(t, s)
}
