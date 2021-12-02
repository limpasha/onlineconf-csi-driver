# universal-csi-driver

## What it is

This is [Container Storage Interface](https://github.com/container-storage-interface/spec/blob/master/spec.md) driver providing plugable volumes for any kubernetes deployment. These volumes are populated with data provided by any configured sidecar container within driver's pod.

Heavily inspired by and forked (mostly copy-pasted) from [onlineconf-csi-driver](https://github.com/onlineconf/onlineconf-csi-driver) by [@aleksey-mashanov](https://github.com/aleksey-mashanov).

## Why use

For example one has an application in kubernetes (a deployment) which uses some data stored on disk. The data itself is provided by sidecar container for the main application in the pod via shared volume.

If one needs a variety of different apps (different deployments) using the same data, we have no choice but attach the sidecar to each of them. This leads to adding extra load to the source the data is downloaded from. Also application pods are becoming heavier and require more resources.

An alternative way is to have CSI driver with required sidecars as daemonSet on each node. This approach allows to have single sidecar per node so it limits their number to the number of nodes in cluster. 
The data itself is stored localy on each node (in emptyDir volume), which guarantees that files are always available to be accessed (cached) if sidecar container restarts for to any reason or data source is unavailable for data to be downloaded by sidecar on start.

```
             Node                                     Node
┌───────────────────────────────┐      ┌──────────────────────────────────────┐
│                               │      │            kubelet                   │
│           App Pod             │      │   App Pod    ▲    CSI driver Pod     │
│ ┌───────────────────────┐     │      │ ┌──────────┐ ││ ┌──────────────────┐ │
│ │                       │     │      │ │          ├─┘│ │                  │ │
│ │ ┌─► App       Sidecar │     │      │ │          │  │ │                  │ │
│ │ │                  │  │     │      │ │   App    │  └►│ Driver   Sidecar │ │
│ │ │                  │  │     │ ──►  │ │          │    │             │    │ │
│ │ │                  │  │     │      │ │          │    │             │    │ │
│ │ └──────── Volume ◄─┘  │     │      │ └──────────┘    └─────────────┼────┘ │
│ │                       │     │      │      ▲                        │      │
│ └───────────────────────┘     │      │      └─────  Volume  ◄────────┘      │
│                               │      │                                      │
└───────────────────────────────┘      └──────────────────────────────────────┘
Sidecar with data within app's pod        Sidecar with data within driver's pod
```

## How to use

1. Configure driver's pod with needed sidecar containers
   1. Each sidecard container (`sidecar1`, `sidecar2`) must be mounted to the corresponding volume named `universal-csi-driver-data-sidecar*` in order to share its data.
   2. The data must be put to this volume by sidecar container to become available via PersitentVolume by applications
    ```yaml
    ---
    apiVersion: storage.k8s.io/v1beta1
    kind: CSIDriver
    metadata:
    name: csi.universal-csi-driver
    ...
    ---
    kind: DaemonSet
    apiVersion: apps/v1
    metadata:
    name: universal-csi-driver
    template:
        spec:
        containers:
        - name: universal-csi-driver
            ...
          volumeMounts:
            ...
            - name: universal-csi-driver-data-sidecar1
              mountPath: /data/sidecar1
            - name: universal-csi-driver-data-sidecar2
              mountPath: /data/sidecar2
        - name: sidecar1
            ...
            volumeMounts:
            ...
            - name: universal-csi-driver-data-sidecar1
              mountPath: /data1
        - name: sidecar2
            ...
            volumeMounts:
            ...
            - name: universal-csi-driver-data-sidecar2
              mountPath: /data2
        volumes:
          - name: universal-csi-driver-data-sidecar1
            emptyDir: {}
          - name: universal-csi-driver-data-sidecar2
            emptyDir: {}
    ```
    3. Create **Persistent Volumes** and **Persistent Volume Claims** for this driver. This PVC then can be used in application deployments to retreive date written to `universal-csi-driver-data`.
    ```yaml
    ---
    apiVersion: v1
    kind: PersistentVolume
    metadata:
    name: universal-csi-driver-sidecar1-volume
    spec:
    volumeMode: Filesystem
    accessModes:
        - ReadOnlyMany
    capacity:
        storage: 1Gi
    csi:
        driver: csi.universal-csi-driver
        volumeHandle: niversal-csi-driver-sidecar1-volume
        readOnly: true
    ---
    apiVersion: v1
    kind: PersistentVolumeClaim
    metadata:
    name: universal-csi-driver-sidecar1-volume-claim
    spec:
    storageClassName: ''
    accessModes:
        - ReadOnlyMany
    volumeMode: Filesystem
    volumeName: universal-csi-driver-sidecar1-volume
    resources:
        requests:
            storage: 1Gi
    ```
2. Deploy the driver as DaemonSet to needed nodes in cluster (recommended to use `nodeSelector` for the nodes which can serve application deployments).
3. In App deployment which needs data from sidecar, configure volume

```yaml
---
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    containers:
    -name: app
     volumeMounts:
      - name: universal-csi-driver-sidecar1-volume
        mountPath: /data1
    ...
    volumes:
    - name: universal-csi-driver-sidecar1-volume
      persistentVolumeClaim:
        claimName: universal-csi-driver-sidecar1-volume-claim

```

So, the scheme of pods and volumes within a node is:

- Each sidecar puts its data to corresponding EmptyDir volume, attached to this sidecar **and universal-csi-driver container**
- PVs are attached via CSI protocol to the mounts inside universal-csi-driver container (which are those EmptyDir volumes the data was put to)
- App Pod attached this PVs as volumes to itself

*Finally, EmptyDir volumes from CSI driver's Pod are available in App Pod as PVs.*

```
         CSI driver Pod
 ┌───────────────────────────────┐
 │ universal-csi-driver          │
 │    ▲    ▲          │          │
 │    │    │          └──────────┼──────┬────────┐
 │    │ ┌──┴─────────┐           │      │        │
 │    │ │EmptyDir 1  │◄──sidecar1│      ▼        ▼
 │    │ └────────────┘           │ ┌── PV1      PV2 ───┐
 │    │                          │ │                   │
 │ ┌──┴─────────┐                │ │     App Pod       │
 │ │EmptyDir 2  │◄──sidecar2     │ │ ┌─────────────┐   │
 │ └────────────┘                │ └►│volume 1     │   │
 │                               │   │     volume 2│◄──┘
 └───────────────────────────────┘   └─────────────┘
```

Full example deploy can be found [here](deploy.yaml).

# How it works internally

First of all, [long story](https://github.com/container-storage-interface/spec/blob/master/spec.md) short about CSI drivers for kubernetes.

1. One creates a storage of kind `CSIDriver` with name `csi.universal-csi-driver` in a kubernetes cluster.
2. Then the DaemonSet which communicates with kubelet via so called "CSI protocol" is created. It uses sidecar `csi-node-driver-registrar` to register itself as backend for  `csi.universal-csi-driver`.
3. One creates Persistent Volume (PV) with type `csi` and pointing to specific driver `csi.universal-csi-driver` (let's talk about static provisioning only).
4. In order to use PV [one needs](https://kubernetes.io/docs/concepts/storage/persistent-volumes/) Persistent Volume Claim (PVC) to be bound with corresponding PV.
5. Any deployment willing to use the PV specifies corresponding PVC's name in its own manifest.
6. When this deployment is being scheduled to a node, two RPC calls are performed to CSI driver via unix socket by kubelet
    1. `NodeStageVolume` with (only meaningful params for us are listed)
        - volume_id (id of PV, unique)
        - staging_target_path (a path inside driver's container for volume to be staged - guaranteed to be unique for each PV)
        - volume_context (any additional data)
    2. `NodePublishVolume` with
        - staging_target_path (the same as above)
        - target_path (the path inside driver's container which is a mount for PV)
        - volume_context (the same as above)
7. After all, the deployment is deployed and data from `target_path` inside driver's container is available via attached PV in deployment's containers.

