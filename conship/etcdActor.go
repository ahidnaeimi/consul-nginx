package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"

	clientv3 "go.etcd.io/etcd/client/v3"
)

type Etcd struct {
	ctx context.Context
}

func (etcd Etcd) Add(serviceInfo ServiceInfo) error {
	serviceInfoJSON, err := json.Marshal(serviceInfo)
	if err != nil {
		log.Printf("Failed to marshal container info: %v", err)
		return err
	}
	etcdKey := fmt.Sprintf("%s/%s", containerName, serviceInfo.SvcName)
	// درج مقدار جدید
	_, err = etcdClient.Put(etcd.ctx, etcdKey, string(serviceInfoJSON), clientv3.WithPrevKV())
	if err != nil {
		log.Printf("Failed to save container info to etcd: %v", err)
	} else {
		log.Printf("Successfully saved container info to etcd for service %s", serviceInfo.SvcName)
	}
	return err
}

// func (etcd Etcd) Get(serviceName string) {
// 	etcdKey := fmt.Sprintf("%s/%s", containerName, serviceName)
// 	oldResp, err := etcdClient.Get(etcd.ctx, etcdKey)
// 	if err != nil {
// 		log.Printf("Failed to get previous value: %v", err)
// 	} else {
// 		if len(oldResp.Kvs) > 0 {
// 			log.Printf("Previous value for key %s: %s", etcdKey, string(oldResp.Kvs[0].Value))
// 		}
// 	}
// }

func (etcd Etcd) GetPrev(serviceName string) (ServiceInfo, error) {
	etcdKey := fmt.Sprintf("%s/%s", containerName, serviceName)

	// گرفتن مقدار فعلی
	currentResp, err := etcdClient.Get(etcd.ctx, etcdKey)
	if err != nil {
		return ServiceInfo{}, fmt.Errorf("failed to get current value: %w", err)
	}
	if len(currentResp.Kvs) == 0 {
		return ServiceInfo{}, fmt.Errorf("key not found: %s", etcdKey)
	}

	currentRev := currentResp.Kvs[0].ModRevision
	if currentRev <= 1 {
		return ServiceInfo{}, fmt.Errorf("no previous version exists for key: %s", etcdKey)
	}

	// گرفتن مقدار قبلی
	prevResp, err := etcdClient.Get(etcd.ctx, etcdKey, clientv3.WithRev(currentRev-1))
	if err != nil {
		return ServiceInfo{}, fmt.Errorf("failed to get previous value: %w", err)
	}
	if len(prevResp.Kvs) == 0 {
		return ServiceInfo{}, fmt.Errorf("previous version not available (possibly compacted)")
	}

	// دی‌سیریالایز با تبدیل AllowedPorts
	var tmp struct {
		SvcName        string            `json:"SvcName"`
		ContainerName  string            `json:"ContainerName"`
		ContainerID    string            `json:"ContainerID"`
		ServiceAddress string            `json:"ServiceAddress"`
		AllowedPorts   map[string]bool   `json:"AllowedPorts"`
		Tags           []string          `json:"Tags"`
	}
	err = json.Unmarshal(prevResp.Kvs[0].Value, &tmp)
	if err != nil {
		return ServiceInfo{}, fmt.Errorf("failed to decode: %w", err)
	}

	allowed := make(map[int]bool)
	for k, v := range tmp.AllowedPorts {
		if port, err := strconv.Atoi(k); err == nil {
			allowed[port] = v
		}
	}

	info := ServiceInfo{
		SvcName:        tmp.SvcName,
		ContainerName:  tmp.ContainerName,
		ContainerID:    tmp.ContainerID,
		ServiceAddress: tmp.ServiceAddress,
		AllowedPorts:   allowed,
		Tags:           tmp.Tags,
	}
	return info, nil
}



func (etcd Etcd) watchKeyHistory() {
	rch := etcdClient.Watch(etcd.ctx, "conship/", clientv3.WithPrefix(), clientv3.WithPrevKV(), clientv3.WithRev(0))
	for wresp := range rch {
		for _, ev := range wresp.Events {
			// فقط می‌تونی اینجا اطلاعات رو بریزی توی cache یا DB مثلاً
			if ev.Type == clientv3.EventTypePut {
				if ev.PrevKv != nil {
					// handle previous value if needed
				}
			} else if ev.Type == clientv3.EventTypeDelete {
				// handle deleted value if needed
			}
		}
	}
}



