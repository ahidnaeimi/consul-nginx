package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/docker/docker/client"
)

func consulRegistered(cli *client.Client, svcName string, containerID string, serviceAddress string, port int, tags []string) {
	serviceID := fmt.Sprintf("%s-%d", containerID[:12], port)
	serviceDef := map[string]interface{}{
		"ID":      serviceID,
		"Name":    svcName,
		"Address": serviceAddress,
		"Port":    port,
		"Tags":    tags,
		"Check": map[string]interface{}{
			"TCP":                            fmt.Sprintf("%s:%d", serviceAddress, port),
			"Interval":                       "10s",
			"Timeout":                        "1s",
			"DeregisterCriticalServiceAfter": "1m",
		},
	}
	payload, _ := json.Marshal(serviceDef)
	req, _ := http.NewRequest("PUT", fmt.Sprintf("%s/v1/agent/service/register", consulAddress), strings.NewReader(string(payload)))
	if consulToken != "" {
		req.Header.Set("X-Consul-Token", consulToken)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		resp.Body.Close()
	}
	log.Printf("Service %s on port %d registered", svcName, port)
}

func consulDeregistered(serviceName string, serviceID string) {
	agentURL := fmt.Sprintf("%s/v1/agent/service/deregister/%s", consulAddress, serviceID)
	req, err := http.NewRequest("PUT", agentURL, nil)
	if err != nil {
		log.Printf("Failed to create deregister request: %v", err)
		return
	}
	if consulToken != "" {
		req.Header.Set("X-Consul-Token", consulToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Failed to deregister service from agent: %v", err)
	} else {
		resp.Body.Close()
		log.Printf("STOP-Deregistered service %s from agent (status %d)", serviceID, resp.StatusCode)
	}

	// حذف از Catalog
	catalogURL := fmt.Sprintf("%s/v1/catalog/service/%s", consulAddress, serviceName)
	catalogReq, err := http.NewRequest("GET", catalogURL, nil)
	if err != nil {
		log.Printf("Failed to create catalog request: %v", err)
		return
	}
	if consulToken != "" {
		catalogReq.Header.Set("X-Consul-Token", consulToken)
	}
	catalogResp, err := http.DefaultClient.Do(catalogReq)
	if err != nil {
		log.Printf("Failed to get service from catalog: %v", err)
		return
	}
	defer catalogResp.Body.Close()

	if catalogResp.StatusCode == http.StatusOK {
		var services []map[string]interface{}
		if err := json.NewDecoder(catalogResp.Body).Decode(&services); err != nil {
			log.Printf("Failed to decode catalog response: %v", err)
			return
		}

		for _, service := range services {
			if myserviceID, ok := service["ServiceID"].(string); ok && myserviceID == serviceID {
				catalogDeregisterURL := fmt.Sprintf("%s/v1/catalog/deregister", consulAddress)
				deregisterPayload := map[string]interface{}{
					"Node":      service["Node"],
					"ServiceID": myserviceID,
				}
				payload, _ := json.Marshal(deregisterPayload)
				catalogDeregisterReq, err := http.NewRequest("PUT", catalogDeregisterURL, strings.NewReader(string(payload)))
				if err != nil {
					log.Printf("Failed to create catalog deregister request: %v", err)
					continue
				}
				if consulToken != "" {
					catalogDeregisterReq.Header.Set("X-Consul-Token", consulToken)
				}
				catalogDeregisterResp, err := http.DefaultClient.Do(catalogDeregisterReq)
				if err != nil {
					log.Printf("Failed to deregister service from catalog: %v", err)
				} else {
					catalogDeregisterResp.Body.Close()
					log.Printf("STOP-Deregistered service %s from catalog (status %d)", myserviceID, catalogDeregisterResp.StatusCode)
				}
			}
		}
	}
}

func serviceHealthCheck(svcName string) bool {
	// منتظر ماندن تا سرویس healthy شود
	// maxRetries := 30 // 30 بار تلاش با فاصله 2 ثانیه = 60 ثانیه
	// healthy := false
	// for i := 0; i < maxRetries; i++ {
	// چک کردن سلامت سرویس
	url := fmt.Sprintf("%s/v1/agent/health/service/name/%s", consulAddress, svcName)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Printf("Failed to create health check request: %v", err)
		return false
	}
	if consulToken != "" {
		req.Header.Set("X-Consul-Token", consulToken)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("Failed to check service health: %v", err)
		time.Sleep(2 * time.Second)
		return false
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		log.Printf("Failed to read health check response: %v", err)
		time.Sleep(2 * time.Second)
		return false
	}

	var health []map[string]interface{}
	if err := json.Unmarshal(body, &health); err != nil {
		log.Printf("Failed to parse health check response: %v", err)
		time.Sleep(2 * time.Second)
		return false
	}

	allHealthy := true
	for _, check := range health {
		if status, ok := check["Status"].(string); ok {
			if status != "passing" {
				allHealthy = false
				break
			}
		}
	}

	if allHealthy {
		log.Printf("Service %s is now healthy", svcName)
		return true
	}
	return false
	// 	log.Printf("Waiting for service %s to become healthy... (attempt %d/%d)", svcName, i+1, maxRetries)
	// 	time.Sleep(2 * time.Second)
	// }

}
