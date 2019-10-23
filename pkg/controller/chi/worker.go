// Copyright 2019 Altinity Ltd and/or its affiliates. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package chi

import (
	"fmt"

	chop "github.com/altinity/clickhouse-operator/pkg/apis/clickhouse.altinity.com/v1"
	chopmodels "github.com/altinity/clickhouse-operator/pkg/model"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/golang/glog"
)

type worker struct {
	c          *Controller
	normalizer *chopmodels.Normalizer
	schemer    *chopmodels.Schemer
	creator    *chopmodels.Creator
}

func (c *Controller) newWorker() *worker {
	return &worker{
		c:          c,
		normalizer: chopmodels.NewNormalizer(c.chopConfigManager.Config()),
		schemer: chopmodels.NewSchemer(
			c.chopConfigManager.Config().ChUsername,
			c.chopConfigManager.Config().ChPassword,
			c.chopConfigManager.Config().ChPort,
		),
		creator: nil,
	}
}

// processWorkItem processes one work item according to its type
func (w *worker) processItem(item interface{}) error {
	switch item.(type) {

	case *ReconcileChi:
		reconcile, _ := item.(*ReconcileChi)
		switch reconcile.cmd {
		case reconcileAdd:
			return w.addChi(reconcile.new)
		case reconcileUpdate:
			return w.updateChi(reconcile.old, reconcile.new)
		case reconcileDelete:
			return w.deleteChi(reconcile.old)
		}

		// Unknown item type, don't know what to do with it
		// Just skip it and behave like it never existed
		utilruntime.HandleError(fmt.Errorf("unexpected reconcile - %#v", reconcile))
		return nil

	case *ReconcileChit:
		reconcile, _ := item.(*ReconcileChit)
		switch reconcile.cmd {
		case reconcileAdd:
			return w.c.addChit(reconcile.new)
		case reconcileUpdate:
			return w.c.updateChit(reconcile.old, reconcile.new)
		case reconcileDelete:
			return w.c.deleteChit(reconcile.old)
		}

		// Unknown item type, don't know what to do with it
		// Just skip it and behave like it never existed
		utilruntime.HandleError(fmt.Errorf("unexpected reconcile - %#v", reconcile))
		return nil

	case *ReconcileChopConfig:
		reconcile, _ := item.(*ReconcileChopConfig)
		switch reconcile.cmd {
		case reconcileAdd:
			return w.c.addChopConfig(reconcile.new)
		case reconcileUpdate:
			return w.c.updateChopConfig(reconcile.old, reconcile.new)
		case reconcileDelete:
			return w.c.deleteChopConfig(reconcile.old)
		}

		// Unknown item type, don't know what to do with it
		// Just skip it and behave like it never existed
		utilruntime.HandleError(fmt.Errorf("unexpected reconcile - %#v", reconcile))
		return nil

	case *DropDns:
		drop, _ := item.(*DropDns)
		if chi, err := w.createChiFromObjectMeta(drop.initiator); err == nil {
			glog.V(1).Infof("endpointsInformer UpdateFunc(%s/%s) flushing DNS for CHI %s", drop.initiator.Namespace, drop.initiator.Name, chi.Name)
			_ = w.schemer.ChiDropDnsCache(chi)
		} else {
			glog.V(1).Infof("endpointsInformer UpdateFunc(%s/%s) unable to find CHI by %v", drop.initiator.Namespace, drop.initiator.Name, drop.initiator.Labels)
		}
		return nil
	}

	// Unknown item type, don't know what to do with it
	// Just skip it and behave like it never existed
	utilruntime.HandleError(fmt.Errorf("unexpected item in the queue - %#v", item))
	return nil
}

// addChi normalize CHI - updates CHI object to normalized
func (w *worker) addChi(new *chop.ClickHouseInstallation) error {
	// CHI is a new one - need to create normalized CHI
	// Operator receives CHI struct partially filled by data from .yaml file provided by user
	// We need to create full normalized specification
	glog.V(1).Infof("addChi(%s/%s)", new.Namespace, new.Name)
	w.c.eventChi(new, eventTypeNormal, eventActionCreate, eventReasonCreateCompleted, fmt.Sprintf("ClickHouseInstallation (%s): start add process", new.Name))

	return w.updateChi(nil, new)
}

// updateChi sync CHI which was already created earlier
func (w *worker) updateChi(old, new *chop.ClickHouseInstallation) error {

	if old == nil {
		old, _ = w.normalizer.CreateTemplatedChi(&chop.ClickHouseInstallation{}, false)
	} else if !old.IsNormalized() {
		old, _ = w.normalizer.CreateTemplatedChi(old, true)
		//			if err != nil {
		//				glog.V(1).Infof("ClickHouseInstallation (%s): unable to normalize: %q", chi.Name, err)
		//				c.eventChi(chi, eventTypeError, eventActionCreate, eventReasonCreateFailed, "unable to normalize configuration")
		//				return err
		//			}
	}

	if new == nil {
		new, _ = w.normalizer.CreateTemplatedChi(&chop.ClickHouseInstallation{}, false)
	} else if !new.IsNormalized() {
		new, _ = w.normalizer.CreateTemplatedChi(new, true)
		//			if err != nil {
		//				glog.V(1).Infof("ClickHouseInstallation (%s): unable to normalize: %q", chi.Name, err)
		//				c.eventChi(chi, eventTypeError, eventActionCreate, eventReasonCreateFailed, "unable to normalize configuration")
		//				return err
		//			}
	}

	if old.ObjectMeta.ResourceVersion == new.ObjectMeta.ResourceVersion {
		glog.V(2).Infof("updateChi(%s/%s): ResourceVersion did not change: %s", new.Namespace, new.Name, new.ObjectMeta.ResourceVersion)
		// No need to react

		// Update hostnames list for monitor
		w.c.updateWatch(new.Namespace, new.Name, chopmodels.CreatePodFQDNsOfChi(new))

		return nil
	}

	glog.V(2).Infof("updateChi(%s/%s)", new.Namespace, new.Name)

	actionPlan := NewActionPlan(old, new)

	if actionPlan.IsNoChanges() {
		glog.V(2).Infof("updateChi(%s/%s): no changes found", new.Namespace, new.Name)
		// No need to react
		return nil
	}

	glog.V(1).Infof("updateChi(%s/%s) - start reconcile", new.Namespace, new.Name)

	// We are going to update CHI
	// Write declared CHI with initialized .Status, so it would be possible to monitor progress
	// Write normalized/expanded version
	new.Status.Status = chop.StatusInProgress
	new.Status.UpdatedHostsCount = 0
	new.Status.AddedHostsCount = 0
	new.Status.DeletedHostsCount = 0
	new.Status.DeleteHostsCount = actionPlan.GetRemovedHostsNum()
	_ = w.c.updateChiObject(new)

	if err := w.reconcile(new); err != nil {
		log := fmt.Sprintf("Update of resources has FAILED: %v", err)
		glog.V(1).Info(log)
		w.c.eventChi(new, eventTypeError, eventActionUpdate, eventReasonUpdateFailed, log)
		return nil
	}

	// Post-process added items
	actionPlan.WalkAdded(
		func(cluster *chop.ChiCluster) {
			log := fmt.Sprintf("Added cluster %s", cluster.Name)
			glog.V(1).Info(log)
			w.c.eventChi(new, eventTypeNormal, eventActionUpdate, eventReasonUpdateInProgress, log)
		},
		func(shard *chop.ChiShard) {
			log := fmt.Sprintf("Added shard %d to cluster %s", shard.Address.ShardIndex, shard.Address.ClusterName)
			glog.V(1).Info(log)
			w.c.eventChi(new, eventTypeNormal, eventActionUpdate, eventReasonUpdateInProgress, log)

			_ = w.createTablesOnShard(new, shard)
		},
		func(host *chop.ChiHost) {
			log := fmt.Sprintf("Added replica %d to shard %d in cluster %s", host.Address.ReplicaIndex, host.Address.ShardIndex, host.Address.ClusterName)
			glog.V(1).Info(log)
			w.c.eventChi(new, eventTypeNormal, eventActionUpdate, eventReasonUpdateInProgress, log)

			_ = w.createTablesOnHost(new, host)
		},
	)

	// Remove deleted items
	actionPlan.WalkRemoved(
		func(cluster *chop.ChiCluster) {
			w.c.eventChi(old, eventTypeNormal, eventActionUpdate, eventReasonUpdateInProgress, fmt.Sprintf("delete cluster %s", cluster.Name))
			_ = w.deleteCluster(cluster)
		},
		func(shard *chop.ChiShard) {
			w.c.eventChi(old, eventTypeNormal, eventActionUpdate, eventReasonUpdateInProgress, fmt.Sprintf("delete shard %d in cluster %s", shard.Address.ShardIndex, shard.Address.ClusterName))
			_ = w.deleteShard(shard)
		},
		func(host *chop.ChiHost) {
			w.c.eventChi(old, eventTypeNormal, eventActionUpdate, eventReasonUpdateInProgress, fmt.Sprintf("delete replica %d from shard %d in cluster %s", host.Address.ReplicaIndex, host.Address.ShardIndex, host.Address.ClusterName))
			_ = w.deleteHost(host)
		},
	)

	// Update CHI object
	new.Status.Status = chop.StatusCompleted
	_ = w.c.updateChiObjectStatus(new)

	//c.metricsExporter.UpdateWatch(new.Namespace, new.Name, chopmodels.CreatePodFQDNsOfChi(new))
	w.c.updateWatch(new.Namespace, new.Name, chopmodels.CreatePodFQDNsOfChi(new))

	return nil
}

// reconcile reconciles ClickHouseInstallation
func (w *worker) reconcile(chi *chop.ClickHouseInstallation) error {
	w.creator = chopmodels.NewCreator(chi, w.c.chopConfigManager.Config(), w.c.version)
	return chi.WalkTillError(
		w.reconcileChi,
		w.reconcileCluster,
		w.reconcileShard,
		w.reconcileHost,
	)
}

// reconcileChi reconciles CHI global objects
func (w *worker) reconcileChi(chi *chop.ClickHouseInstallation) error {
	// 1. CHI Service
	service := w.creator.CreateServiceChi()
	if err := w.c.ReconcileService(service); err != nil {
		return err
	}

	// 2. CHI ConfigMaps

	// ConfigMap common for all resources in CHI
	// contains several sections, mapped as separated chopConfig files,
	// such as remote servers, zookeeper setup, etc
	configMapCommon := w.creator.CreateConfigMapChiCommon()
	if err := w.c.ReconcileConfigMap(configMapCommon); err != nil {
		return err
	}

	// ConfigMap common for all users resources in CHI
	configMapUsers := w.creator.CreateConfigMapChiCommonUsers()
	if err := w.c.ReconcileConfigMap(configMapUsers); err != nil {
		return err
	}

	// Add here other CHI components to be reconciled

	return nil
}

// reconcileCluster reconciles Cluster, excluding nested shards
func (w *worker) reconcileCluster(cluster *chop.ChiCluster) error {
	// Add Cluster's Service
	if service := w.creator.CreateServiceCluster(cluster); service != nil {
		return w.c.ReconcileService(service)
	} else {
		return nil
	}
}

// reconcileShard reconciles Shard, excluding nested replicas
func (w *worker) reconcileShard(shard *chop.ChiShard) error {
	// Add Shard's Service
	if service := w.creator.CreateServiceShard(shard); service != nil {
		return w.c.ReconcileService(service)
	} else {
		return nil
	}
}

// reconcileHost reconciles ClickHouse host
func (w *worker) reconcileHost(host *chop.ChiHost) error {
	// Add host's Service
	service := w.creator.CreateServiceHost(host)
	if err := w.c.ReconcileService(service); err != nil {
		return err
	}

	// Add host's ConfigMap
	configMap := w.creator.CreateConfigMapHost(host)
	if err := w.c.ReconcileConfigMap(configMap); err != nil {
		return err
	}

	// Add host's StatefulSet
	statefulSet := w.creator.CreateStatefulSet(host)
	if err := w.c.ReconcileStatefulSet(statefulSet, host); err != nil {
		return err
	}

	return nil
}

// createTablesOnHost
// TODO move this into Schemer
func (w *worker) createTablesOnHost(chi *chop.ClickHouseInstallation, host *chop.ChiHost) error {
	cluster := &chi.Spec.Configuration.Clusters[host.Address.ClusterIndex]

	names, createSQLs, _ := w.schemer.GetCreateReplicatedObjects(chi, cluster, host)
	glog.V(1).Infof("Creating replicated objects: %v", names)
	_ = w.schemer.HostApplySQLs(host, createSQLs, true)

	names, createSQLs, _ = w.schemer.ClusterGetCreateDistributedObjects(chi, cluster)
	glog.V(1).Infof("Creating distributed objects: %v", names)
	_ = w.schemer.HostApplySQLs(host, createSQLs, true)

	return nil
}

// createTablesOnShard
// TODO move this into Schemer
func (w *worker) createTablesOnShard(chi *chop.ClickHouseInstallation, shard *chop.ChiShard) error {
	cluster := &chi.Spec.Configuration.Clusters[shard.Address.ClusterIndex]

	names, createSQLs, _ := w.schemer.ClusterGetCreateDistributedObjects(chi, cluster)
	glog.V(1).Infof("Creating distributed objects: %v", names)
	_ = w.schemer.ShardApplySQLs(shard, createSQLs, true)

	return nil
}

// deleteTablesOnHost deletes ClickHouse tables on a host
// TODO move this into Schemer
func (w *worker) deleteTablesOnHost(host *chop.ChiHost) error {
	// Delete tables on replica
	tableNames, dropTableSQLs, _ := w.schemer.HostGetDropTables(host)
	glog.V(1).Infof("Drop tables: %v as %v", tableNames, dropTableSQLs)
	_ = w.schemer.HostApplySQLs(host, dropTableSQLs, false)

	return nil
}

// deleteChi deletes all kubernetes resources related to chi *chop.ClickHouseInstallation
func (w *worker) deleteChi(chi *chop.ClickHouseInstallation) error {
	var err error

	glog.V(1).Infof("Start delete CHI %s/%s", chi.Namespace, chi.Name)

	chi, err = w.normalizer.CreateTemplatedChi(chi, true)
	if err != nil {
		glog.V(1).Infof("ClickHouseInstallation (%q): unable to normalize: %q", chi.Name, err)
		return err
	}

	// Delete all clusters
	chi.WalkClusters(func(cluster *chop.ChiCluster) error {
		return w.deleteCluster(cluster)
	})

	// Delete ConfigMap(s)
	err = w.c.deleteConfigMapsChi(chi)

	// Delete Service
	err = w.c.deleteServiceChi(chi)

	w.c.eventChi(chi, eventTypeNormal, eventActionDelete, eventReasonDeleteCompleted, "deleted")

	glog.V(1).Infof("End delete CHI %s/%s", chi.Namespace, chi.Name)

	return nil
}

// deleteHost deletes all kubernetes resources related to replica *chop.ChiHost
func (w *worker) deleteHost(host *chop.ChiHost) error {
	// Each host consists of
	// 1. Tables on host - we need to delete tables on the host in order to clean Zookeeper data
	// 2. StatefulSet
	// 3. PersistentVolumeClaim
	// 4. ConfigMap
	// 5. Service
	// Need to delete all these item
	glog.V(1).Infof("Worker delete host %s/%s", host.Address.ClusterName, host.Name)

	_ = w.deleteTablesOnHost(host)

	return w.c.deleteHost(host)
}

// deleteShard deletes all kubernetes resources related to shard *chop.ChiShard
func (w *worker) deleteShard(shard *chop.ChiShard) error {
	glog.V(1).Infof("Start delete shard %s/%s", shard.Address.Namespace, shard.Name)

	// Delete all replicas
	shard.WalkHosts(w.deleteHost)

	// Delete Shard Service
	_ = w.c.deleteServiceShard(shard)
	glog.V(1).Infof("End delete shard %s/%s", shard.Address.Namespace, shard.Name)

	return nil
}

// deleteCluster deletes all kubernetes resources related to cluster *chop.ChiCluster
func (w *worker) deleteCluster(cluster *chop.ChiCluster) error {
	glog.V(1).Infof("Start delete cluster %s/%s", cluster.Address.Namespace, cluster.Name)

	// Delete all shards
	cluster.WalkShards(w.deleteShard)

	// Delete Cluster Service
	_ = w.c.deleteServiceCluster(cluster)
	glog.V(1).Infof("End delete cluster %s/%s", cluster.Address.Namespace, cluster.Name)

	return nil
}

func (w *worker) createChiFromObjectMeta(objectMeta *meta.ObjectMeta) (*chop.ClickHouseInstallation, error) {
	chi, err := w.c.GetChiByObjectMeta(objectMeta)
	if err != nil {
		return nil, err
	}

	chi, err = w.normalizer.DoChi(chi)
	if err != nil {
		return nil, err
	}

	return chi, nil
}

func (w *worker) createClusterFromObjectMeta(objectMeta *meta.ObjectMeta) (*chop.ChiCluster, error) {
	clusterName, err := chopmodels.GetClusterNameFromObjectMeta(objectMeta)
	if err != nil {
		return nil, fmt.Errorf("ObjectMeta %s does not generated by CHI %v", objectMeta.Name, err)
	}

	chi, err := w.createChiFromObjectMeta(objectMeta)
	if err != nil {
		return nil, err
	}

	cluster := chi.FindCluster(clusterName)
	if cluster == nil {
		return nil, fmt.Errorf("can't find cluster %s in CHI %s", clusterName, chi.Name)
	}

	return cluster, nil
}
