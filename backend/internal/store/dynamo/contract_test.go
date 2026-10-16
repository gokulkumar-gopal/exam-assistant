package dynamo

import (
	"testing"
	"time"

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

func TestJobStoreContract(t *testing.T) {
	storetest.RunJobStoreSuite(t, func(t *testing.T) store.JobStore {
		s, _ := newStores()
		s.Now = func() time.Time { return storetest.T0 }
		return s
	})
}

func TestExamStoreContract(t *testing.T) {
	storetest.RunExamStoreSuite(t, func(t *testing.T) store.ExamStore { s, _ := newStores(); return s })
}

func TestAttemptStoreContract(t *testing.T) {
	storetest.RunAttemptStoreSuite(t, func(t *testing.T) store.AttemptStore { s, _ := newStores(); return s })
}
