package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"

	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/medik8s/fence-agents-remediation/api/v1alpha1"
	"github.com/medik8s/fence-agents-remediation/pkg/utils"
)

type Executer struct {
	client.Client
	log      logr.Logger
	routines map[types.UID]bool
	mapLock  sync.Mutex
	runner   runnerFunc
}

// runnerFunc is a function that runs the command and returns the stdout, stderr and error
// it is configurable in Executer for testing purposes
type runnerFunc func(ctx context.Context, uid types.UID, command []string, logger logr.Logger) (string, string, error)

// NewExecuter builds the Executer
func NewExecuter(client client.Client) (*Executer, error) {
	logger := ctrl.Log.WithName("executer")

	return &Executer{
		Client:   client,
		log:      logger,
		routines: make(map[types.UID]bool),
		runner:   run,
	}, nil
}

// NewExecuter builds an Executer with configurable runnerFunc for testing
func NewFakeExecuter(client client.Client, fn runnerFunc) (*Executer, error) {
	logger := ctrl.Log.WithName("fakeExecuter")

	return &Executer{
		Client:   client,
		log:      logger,
		routines: make(map[types.UID]bool),
		runner:   fn,
	}, nil
}

// AsyncExecute runs the command in a goroutine mapped to the UID
func (e *Executer) AsyncExecute(ctx context.Context, uid types.UID, command []string, retryCount int, retryDuration, timeout time.Duration) {
	e.mapLock.Lock()
	defer e.mapLock.Unlock()
	if _, exist := e.routines[uid]; !exist {
		e.routines[uid] = true

		go func(uid types.UID, command []string) {

			// Linear backoff
			backoff := wait.Backoff{
				Steps:    retryCount,
				Duration: retryDuration,
				Factor:   1.0,
			}

			var stdout, stderr string
			var fa_err error

			e.log.Info("fence agent start", "uid", uid, "command", command)
			err := wait.ExponentialBackoffWithContext(ctx,
				backoff,
				func(ctx context.Context) (bool, error) {
					fa_ctx, cancel := context.WithTimeout(ctx, timeout)
					defer cancel()
					stdout, stderr, fa_err = e.runner(fa_ctx, uid, command, e.log)
					if fa_err != nil {
						if wait.Interrupted(fa_err) {
							e.log.Error(fa_err, "fence agent timeout", "uid", uid)
							return false, fa_err
						}
						e.log.Info("command failed", "uid", uid, "response", stdout, "errMessage", stderr, "err", fa_err)
						return false, nil
					}
					e.log.Info("command completed", "uid", uid, "response", stdout, "errMessage", stderr, "err", fa_err)
					return true, nil
				})

			if wait.Interrupted(err) {
				e.log.Error(err, "command timed out", "uid", uid)
				fa_err = err
			} else {
				e.log.Info("fence agent done", "uid", uid, "command", command, "stdout", stdout, "stderr", stderr, "err", fa_err)
			}

			// loop to handle only the updateStatus error cases where:
			// - FAR cannot be found, but it does exist
			// - the status update fails for conflicts
			for {
				far, err := e.getFenceAgentsRemediationByUID(ctx, uid)
				if err != nil {
					if !apiErrors.IsNotFound(err) {
						continue
					}
					e.log.Error(err, "could not update status", "FAR uid", uid)
					break
				}

				err = e.updateStatus(ctx, far, fa_err)
				if err == nil {
					e.log.Info("status updated", "FAR uid", uid)
					break
				}
				if apiErrors.IsConflict(err) {
					e.log.Error(err, "conflict while updating the status", "FAR uid", uid)
					time.Sleep(1 * time.Second)
				}
				e.log.Error(err, "failed to update status", "FAR uid", uid)
				break
			}
		}(uid, command)
	}
}

// Exists checks if there is already a running Fence Agent command mapped to the UID
func (e *Executer) Exists(uid types.UID) bool {
	e.mapLock.Lock()
	defer e.mapLock.Unlock()
	_, exist := e.routines[uid]
	return exist
}

// run runs the command in the container and updates the status of the FAR instance maching the UID
func run(ctx context.Context, uid types.UID, command []string, logger logr.Logger) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)

	var outBuilder, errBuilder strings.Builder
	cmd.Stdout = &outBuilder
	cmd.Stderr = &errBuilder

	err = cmd.Run()

	return outBuilder.String(), errBuilder.String(), err
}

func (e *Executer) getFenceAgentsRemediationByUID(ctx context.Context, uid types.UID) (*v1alpha1.FenceAgentsRemediation, error) {
	farList := &v1alpha1.FenceAgentsRemediationList{}
	if err := e.List(ctx, farList, &client.ListOptions{}); err != nil || len(farList.Items) == 0 {
		e.log.Error(err, "failed to list FAR", "FAR uid", uid)
		return nil, err
	}

	for _, far := range farList.Items {
		if far.UID == uid {
			return &far, nil
		}
	}

	err := fmt.Errorf("could not find any FAR matching the UID")
	e.log.Error(err, "failed to get far", "uid", uid)

	return nil, err
}

func (e *Executer) updateStatus(ctx context.Context, far *v1alpha1.FenceAgentsRemediation, err error) error {
	var reason utils.ConditionsChangeReason

	if err == nil {
		reason = utils.FenceAgentSucceeded
	} else if wait.Interrupted(err) {
		reason = utils.FenceAgentTimedOut
	} else {
		reason = utils.FenceAgentFailed
	}

	err = utils.UpdateConditions(reason, far, e.log)
	if err != nil {
		e.log.Error(err, "failed to update conditions", "FAR uid", far.UID)
		return err
	}
	return e.Status().Update(ctx, far)
}
