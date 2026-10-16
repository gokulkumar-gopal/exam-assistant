package dynamo

import (
	"testing"

	"github.com/gokulkumar/exam-assistant/backend/internal/store"
	"github.com/gokulkumar/exam-assistant/backend/internal/store/storetest"
)

// The shared contract suites run against the DynamoDB implementation through the
// in-process fake.
func newStores() (*Stores, *fakeDynamo) {
	f := newFake()
	return New(f, "test"), f
}

func TestUserStoreContract(t *testing.T) {
	storetest.RunUserStoreSuite(t, func(t *testing.T) store.UserStore { s, _ := newStores(); return s })
}

func TestDocStoreContract(t *testing.T) {
	storetest.RunDocStoreSuite(t, func(t *testing.T) store.DocStore { s, _ := newStores(); return s })
}
