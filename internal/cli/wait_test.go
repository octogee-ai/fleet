package cli

import (
	"io"
	"testing"
)

func TestTheSpinnerKeepsSpinningWhileAnyReadIsWaiting(t *testing.T) {
	waiting := &spinner{out: io.Discard}
	waiting.start("first read")
	waiting.start("second read")
	waiting.end()
	if waiting.stop == nil {
		t.Fatal("the spinner stopped while a read was still waiting")
	}
	waiting.end()
	if waiting.stop != nil {
		t.Fatal("the spinner kept spinning after every read finished")
	}
}
