package cli

import (
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewExecuter builds an Executer with configurable runnerFunc for testing
func NewFakeExecuter(client client.Client, fn runnerFunc) (*Executer, *record.FakeRecorder, error) {
	logger := ctrl.Log.WithName("fakeExecuter")
	fakeRecorder := record.NewFakeRecorder(10)

	return &Executer{
		Client:   client,
		log:      logger,
		routines: make(map[types.UID]*routine),
		runner:   fn,
		recorder: fakeRecorder,
	}, fakeRecorder, nil
}
