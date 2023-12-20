package cli

import (
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewExecuter builds an Executer with configurable runnerFunc for testing
func NewFakeExecuter(client client.Client, fn runnerFunc) (*Executer, error) {
	logger := ctrl.Log.WithName("fakeExecuter")

	return &Executer{
		Client:   client,
		log:      logger,
		routines: make(map[types.UID]*routine),
		runner:   fn,
	}, nil
}
