package data

import (
	"testing"
	"encoding/json"
	"strings"

	//"github.com/vocdoni/go-dvote/log"
)

func TestPublishAndRetrieve(t *testing.T) {
	t.Log("Testing adding json")

	exampleVote := votePacket{
		000001,
		"12309801002",
		"nynnynnnynnnyy",
		"132498-0-02103908",
	}

	testObject, err := json.Marshal(exampleVote)
	if err != nil {
		t.Errorf("Bad test JSON: %s", err)
	}
	prepub := string(testObject)

	hash := publish(testObject)
	content := retrieve(hash)
	postpub := string(content)
	//log.Info(hash)
	//log.Info(content)
	if strings.Compare(prepub,postpub) != 0 {
		t.Errorf("Published file doesn't match. Expected:\n %s \n Got: \n %s \n", prepub, postpub)
	}
}
