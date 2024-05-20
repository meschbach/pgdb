/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	errors2 "errors"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/jackc/pgx/v5"
	"github.com/meschbach/pgstate"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	pgdbv1alpha1 "github.com/meschbach/pgdb/api/v1alpha1"
)

const (
	finalizer = "pgdb.storage.meschbach.com/finalizer"
)

var trueVar = true
var yes = &trueVar
var falseVar = false
var no = &falseVar

// DatabaseReconciler reconciles a Database object
type DatabaseReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	ControllerName string
}

//+kubebuilder:rbac:groups=pgdb.storage.meschbach.com,resources=databases,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=pgdb.storage.meschbach.com,resources=databases/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=pgdb.storage.meschbach.com,resources=databases/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Database object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.16.0/pkg/reconcile
func (r *DatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	reconcilerLog := log.FromContext(ctx)
	reconcilerLog.Info("processing request")

	db := &pgdbv1alpha1.Database{}
	if err := r.Get(ctx, req.NamespacedName, db); err != nil {
		if errors.IsNotFound(err) {
			reconcilerLog.Info("missing")
			return ctrl.Result{}, nil
		} else {
			reconcilerLog.Error(err, "Failed to retrieve")
			return ctrl.Result{}, err
		}
	}
	db.Status.Ready = false

	if !db.Spec.MatchesController(r.ControllerName) {
		return ctrl.Result{}, nil
	}
	finalizerName := finalizer + "." + r.ControllerName

	// examine DeletionTimestamp to determine if object is under deletion
	if db.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !controllerutil.ContainsFinalizer(db, finalizerName) {
			controllerutil.AddFinalizer(db, finalizerName)
			reconcilerLog.Info("Adding finalizer")
			if err := r.Update(ctx, db); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		reconcilerLog.Info("Deleting")

		//Do we have a secret?
		if db.Status.DatabaseSecretName != nil {
			secret := &v1.Secret{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: db.Namespace, Name: *db.Status.DatabaseSecretName}, secret); err != nil {
				if errors.IsNotFound(err) {
					reconcilerLog.Info("secret was reported as not found, assuming deleted or not created.")
				} else {
					reconcilerLog.Error(err, "failed to get secret, re-enqueuing")
					return ctrl.Result{
						Requeue:      true,
						RequeueAfter: 10 * time.Second,
					}, err
				}
			} else {
				if err := r.Delete(ctx, secret); err != nil {
					reconcilerLog.Error(err, "failed to delete secret, re-enqueuing")
					return ctrl.Result{
						Requeue:      true,
						RequeueAfter: 10 * time.Second,
					}, err
				}
			}
		}

		// The object is being deleted
		if controllerutil.ContainsFinalizer(db, finalizerName) {
			reconcilerLog.Info("Finalizing")
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalResources(ctx, reconcilerLog, db); err != nil {
				reconcilerLog.Error(err, "failed to cleanup resources.")
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}

			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(db, finalizerName)
			if err := r.Update(ctx, db); err != nil {
				return ctrl.Result{}, err
			}
		}

		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	//todo: this should be an exponential backoff
	//todo: I guess the controller manager is capable of watching these for us
	secret := &v1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: db.Spec.ClusterNamespace,
		Name:      db.Spec.ClusterSecret,
	}, secret); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		db.Status.State = pgdbv1alpha1.MissingClusterSecret
		if err := r.Status().Update(ctx, db); err != nil {
			reconcilerLog.Error(err, "unable to update Database status")
			return ctrl.Result{}, err
		}
		reconcilerLog.Info("failed to locate %q in namespace %q, checking in 30 seconds", db.Spec.ClusterSecret, db.Spec.ClusterNamespace)
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: 30 * time.Second,
		}, nil
	}

	clusterConnection, err := connectionConfigFromSecret(secret)
	if err != nil {
		resultingError := errors2.Join(errors2.New("failed to read cluster connection"), err)
		reconcilerLog.Error(resultingError, "Could not make sense of cluster configuration")
		db.Status.ClusterSecretValid = no
		if err := r.Status().Update(ctx, db); err != nil {
			reconcilerLog.Error(err, "unable to update Database status")
		}
		return ctrl.Result{}, resultingError
	}
	db.Status.ClusterSecretValid = yes

	var databaseName, databaseRolePassword string
	outputSecret := &v1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: db.Spec.DatabaseSecret}, outputSecret); err == nil {
		reconcilerLog.Info("Output secret exists, pulling expected configuration")
		db.Status.DatabaseSecretName = &db.Spec.DatabaseSecret

		var dbErr, passwordErr error
		databaseName, dbErr = internalizeSecretElement(outputSecret, "database")
		databaseRolePassword, passwordErr = internalizeSecretElement(outputSecret, "password")
		joined := errors2.Join(passwordErr, dbErr)
		if joined != nil {
			if err := r.Status().Update(ctx, db); err != nil {
				reconcilerLog.Error(err, "unable to update Database status")
			}
			//todo: perhaps it is better to treat this as a destroy then recreate
			reconcilerLog.Error(joined, "failed to extract user, database, and password from existing secret.")
			return ctrl.Result{}, joined
		}
	} else {
		if !errors.IsNotFound(err) {
			reconcilerLog.Error(err, "Unexpected error while attempting to retrieve output secret.  Retrying")
			return ctrl.Result{
				Requeue:      true,
				RequeueAfter: 1 * time.Second,
			}, nil
		}
		reconcilerLog.Info("Output secret does not exist, generating")
		databaseName = req.Namespace + "-" + req.Name
		passwordConfig := pgstate.GenPasswordConfig{
			AllowSpecial: db.Spec.AllowPasswordSpecials,
		}
		databaseRolePassword = pgstate.GeneratePasswordWithConfig(passwordConfig)

		outputSecret.Namespace = db.Namespace
		outputSecret.Name = db.Spec.DatabaseSecret
		outputSecret.ObjectMeta.SetOwnerReferences([]v12.OwnerReference{
			{
				APIVersion:         db.APIVersion,
				Kind:               db.Kind,
				Name:               db.Name,
				UID:                db.UID,
				BlockOwnerDeletion: yes,
			},
		})
		outputSecret.StringData = make(map[string]string)
		outputSecret.StringData["host"] = clusterConnection.Host
		outputSecret.StringData["port"] = fmt.Sprintf("%d", clusterConnection.Port)
		outputSecret.StringData["user"] = databaseName
		outputSecret.StringData["database"] = databaseName
		outputSecret.StringData["password"] = databaseRolePassword

		//todo: this could happen.  retrying for now until we get more info
		if err := r.Create(ctx, outputSecret); err != nil {
			if errors.IsAlreadyExists(err) {
				reconcilerLog.Info("Target database secret created after initially missing, retrying", "problem", err.Error())
			} else {
				reconcilerLog.Error(err, "Failed to create target database secret, retrying")
			}
			return ctrl.Result{
				Requeue:      true,
				RequeueAfter: 1 * time.Second,
			}, nil
		}
		db.Status.DatabaseSecretName = &outputSecret.Name
	}
	db.Status.DatabaseName = databaseName

	if err := pgstate.EnsureDatabase(ctx, clusterConnection, databaseName, databaseRolePassword); err != nil {
		db.Status.Ready = false
		db.Status.Connected = no
		db.Status.State = pgdbv1alpha1.ClusterConnectionFailure
		if err := r.Status().Update(ctx, db); err != nil {
			reconcilerLog.Error(err, "unable to update Database status")
		}

		reconcilerLog.Error(err, "Error in ensuring database connection succeeded", "database.name", databaseName, "database.host", clusterConnection.Host)
		return ctrl.Result{
			Requeue:      true,
			RequeueAfter: 10 * time.Second,
		}, nil
	}
	db.Status.Connected = yes
	db.Status.State = pgdbv1alpha1.Ready
	db.Status.Ready = true
	if err := r.Status().Update(ctx, db); err != nil {
		reconcilerLog.Error(err, "unable to update Database status")
	}

	return ctrl.Result{}, nil
}

func (r *DatabaseReconciler) deleteExternalResources(ctx context.Context, reconcilerLogger logr.Logger, db *pgdbv1alpha1.Database) error {
	var databaseName, roleName string
	accessCredentialsName := types.NamespacedName{
		Namespace: db.Namespace,
		Name:      db.Spec.DatabaseSecret,
	}
	accessCredentials := &v1.Secret{}
	accessCredentialsError := r.Get(ctx, accessCredentialsName, accessCredentials)

	clusterCredentials := &v1.Secret{}
	if clusterCredentialsError := r.Get(ctx, types.NamespacedName{
		Namespace: db.Spec.ClusterNamespace,
		Name:      db.Spec.ClusterSecret,
	}, clusterCredentials); clusterCredentialsError != nil {
		if errors.IsNotFound(clusterCredentialsError) {
			if db.Status.State == pgdbv1alpha1.MissingClusterSecret || (db.Status.ClusterSecretValid != nil && !*db.Status.ClusterSecretValid) {
				reconcilerLogger.Info("Cluster secret was missing, assuming cluster never existed or no longer does")
				return nil
			}
		}
		return errors2.Join(errors2.New("unable to access cluster secret"), clusterCredentialsError)
	}

	var destroySecret bool
	if accessCredentialsError != nil {
		if !errors.IsNotFound(accessCredentialsError) {
			return errors2.Join(errors2.New("unable to access database credentials"), accessCredentialsError)
		}
		//attempt to reconstruct
		databaseName = db.Namespace + "-" + db.Name
		roleName = db.Namespace + "-" + db.Name
		destroySecret = false
	} else {
		var databaseNameError, databaseUserError error
		databaseName, databaseNameError = internalizeSecretElement(accessCredentials, "database")
		roleName, databaseUserError = internalizeSecretElement(accessCredentials, "user")
		if err := errors2.Join(databaseNameError, databaseUserError); err != nil {
			return err
		}
		destroySecret = true
	}

	clusterConnection, err := connectionConfigFromSecret(clusterCredentials)
	if err != nil {
		return err
	}

	reconcilerLogger.Info("Destroying database", "database", databaseName)
	if err := pgstate.DestroyDatabase(ctx, clusterConnection, databaseName); err != nil {
		return err
	}
	reconcilerLogger.Info("Destroying role", "role", roleName)
	if err := pgstate.DestroyRole(ctx, clusterConnection, roleName); err != nil {
		return err
	}

	if destroySecret {
		return client.IgnoreNotFound(r.Delete(ctx, accessCredentials))
	} else {
		return nil
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *DatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&pgdbv1alpha1.Database{}).
		Complete(r)
}

type missingSecretElement struct {
	Element string
}

func (m *missingSecretElement) Error() string {
	return "secret missing " + m.Element
}

func internalizeSecretElement(secret *v1.Secret, data string) (string, error) {
	datum, ok := secret.Data[data]
	if !ok {
		return "", &missingSecretElement{Element: data}
	}
	return string(datum), nil
}

func connectionConfigFromSecret(secret *v1.Secret) (*pgx.ConnConfig, error) {
	host, hostErr := internalizeSecretElement(secret, "host")
	portString, portErr := internalizeSecretElement(secret, "port")
	clusterUser, userErr := internalizeSecretElement(secret, "user")
	clusterPassword, passwordErr := internalizeSecretElement(secret, "password")
	if e := errors2.Join(hostErr, portErr, userErr, passwordErr); e != nil {
		return nil, e
	}
	portInt, err := strconv.ParseInt(portString, 10, 17)
	if err != nil {
		return nil, errors2.Join(errors2.New("can not parse port as uint16"), err)
	}

	clusterConnection, err := pgx.ParseConfig("")
	if err != nil {
		return nil, err
	}
	clusterConnection.Host = host
	clusterConnection.Port = uint16(portInt)
	clusterConnection.User = clusterUser
	clusterConnection.Password = clusterPassword
	clusterConnection.Database = clusterUser

	return clusterConnection, nil
}
