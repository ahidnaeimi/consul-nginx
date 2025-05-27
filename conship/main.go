package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

var (
	consulAddress        = getEnv("CONSUL_HTTP_ADDR", "http://localhost:8500")
	consulToken          = getEnv("CONSUL_HTTP_TOKEN", "29765957-1839-759e-8ed5-e44de35fcc2e")
	registeredContainers = make(map[string]ContainerInfo)
	stateFile            = "registered.json"
	mu                   sync.Mutex
	containerName        string = "conship"
)

type ContainerInfo struct {
	Ports       []int  `json:"ports"`
	ServiceName string `json:"serviceName"`
}

func initContainerName() error {
	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	idPrefix := hostname

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}

	containers, err := cli.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		return err
	}

	for _, container := range containers {
		if strings.HasPrefix(container.ID, idPrefix) {
			containerName = strings.TrimPrefix(container.Names[0], "/")
			return nil
		}
	}

	return fmt.Errorf("container not found with ID prefix: %s", idPrefix)
}

func main() {
	err := initContainerName()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	fmt.Println("Container Name:", containerName)

	loadState()

	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Error creating Docker client: %v", err)
	}

	options := events.ListOptions{
		Filters: filters.NewArgs(filters.Arg("type", "container")),
	}
	messages, errs := cli.Events(ctx, options)
	log.Println("Listening for Docker container events...")

	for {
		select {
		case err := <-errs:
			if err == io.EOF {
				return
			}
			log.Fatalf("Error from Docker events: %v", err)
		case msg := <-messages:
			if msg.Type == events.ContainerEventType {
				switch msg.Action {
				case "start":
					go handleContainerStart(cli, msg.ID)
				case "destroy":
					go handleContainerStop(msg.ID)
				}
			}
		}
	}
}

func handleContainerStart(cli *client.Client, containerID string) {
	ctx := context.Background()

	conshipContainer, err := findContainerByName(cli, ctx, containerName)
	if err != nil {
		log.Printf("Failed to find conship container: %v", err)
		return
	}

	conshipIP := ""
	for _, net := range conshipContainer.NetworkSettings.Networks {
		if net.IPAddress != "" {
			conshipIP = net.IPAddress
			break
		}
	}

	if conshipIP == "" {
		log.Println("Conship container has no IP")
		return
	}

	jsonData, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		log.Printf("Failed to inspect container %s: %v", containerID, err)
		return
	}

	sharedNet := ""
	for name, net := range jsonData.NetworkSettings.Networks {
		for _, cnet := range conshipContainer.NetworkSettings.Networks {
			if net.NetworkID == cnet.NetworkID {
				sharedNet = name
				break
			}
		}
		if sharedNet != "" {
			break
		}
	}
	if sharedNet == "" {
		log.Printf("Container %s is not on the same network as conship", jsonData.Name)
		return
	}

	containerName := strings.TrimPrefix(jsonData.Name, "/")
	serviceAddress := jsonData.NetworkSettings.Networks[sharedNet].IPAddress

	var ports []int
	for portProto := range jsonData.NetworkSettings.Ports {
		port, err := nat.ParsePort(portProto.Port())
		if err != nil {
			log.Printf("Invalid port %s for container %s: %v", portProto.Port(), containerName, err)
			continue
		}
		ports = append(ports, port)

		serviceID := fmt.Sprintf("%s-%d", containerID[:12], port)

		bindings := jsonData.NetworkSettings.Ports[portProto]
		//tags := []string{"docker"}

		envTags := ""
		for _, env := range jsonData.Config.Env {
			if strings.HasPrefix(env, "SERVICE_TAGS=") {
				envTags = strings.TrimPrefix(env, "SERVICE_TAGS=")
				break
			}
		}

		tags := []string{"docker"}
		if len(bindings) > 0 {
			tags = append(tags, "bound")
		} else {
			tags = append(tags, "exposed-only")
		}

		if envTags != "" {
			for _, tag := range strings.Split(envTags, ",") {
				trimmed := strings.TrimSpace(tag)
				if trimmed != "" {
					tags = append(tags, trimmed)
				}
			}
		}


		if len(bindings) > 0 {
			tags = append(tags, "bound")
		} else {
			tags = append(tags, "exposed-only")
		}

		serviceDef := map[string]interface{}{
			"ID":      serviceID,
			"Name":    containerName,
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

		payload, err := json.Marshal(serviceDef)
		if err != nil {
			log.Printf("Failed to marshal service definition: %v", err)
			continue
		}

		req, err := http.NewRequest("PUT", fmt.Sprintf("%s/v1/agent/service/register", consulAddress), strings.NewReader(string(payload)))
		if err != nil {
			log.Printf("Failed to create request: %v", err)
			continue
		}
		if consulToken != "" {
			req.Header.Set("X-Consul-Token", consulToken)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Failed to register service in Consul: %v", err)
			continue
		}
		resp.Body.Close()

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			log.Printf("Service %s on port %d registered successfully in Consul", containerName, port)
		} else {
			log.Printf("Failed to register service %s on port %d, status: %d", containerName, port, resp.StatusCode)
		}
	}

	shortID := containerID[:12]

	mu.Lock()
	registeredContainers[shortID] = ContainerInfo{
		Ports:       ports,
		ServiceName: containerName,
	}
	mu.Unlock()
	saveState()
}

func handleContainerStop(containerID string) {
	shortID := containerID[:12]

	mu.Lock()
	info, ok := registeredContainers[shortID]
	mu.Unlock()

	if !ok {
		log.Printf("No registered info found for container %s", shortID)
		return
	}

	for _, port := range info.Ports {
		serviceID := fmt.Sprintf("%s-%d", shortID, port)

		// حذف از Agent
		req, err := http.NewRequest("PUT", fmt.Sprintf("%s/v1/agent/service/deregister/%s", consulAddress, serviceID), nil)
		if err != nil {
			log.Printf("Failed to create agent deregister request for %s: %v", serviceID, err)
			continue
		}
		if consulToken != "" {
			req.Header.Set("X-Consul-Token", consulToken)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Printf("Failed to deregister %s from agent: %v", serviceID, err)
		} else {
			resp.Body.Close()
			log.Printf("Deregistered service %s from agent (status %d)", serviceID, resp.StatusCode)
		}

		// حذف از Catalog با استفاده از نام سرویس درست:
		catalogURL := fmt.Sprintf("%s/v1/catalog/service/%s", consulAddress, info.ServiceName)
		catalogReq, err := http.NewRequest("GET", catalogURL, nil)
		if err != nil {
			log.Printf("Failed to create catalog lookup request for %s: %v", serviceID, err)
			continue
		}
		if consulToken != "" {
			catalogReq.Header.Set("X-Consul-Token", consulToken)
		}
		catalogResp, err := http.DefaultClient.Do(catalogReq)
		if err != nil {
			log.Printf("Failed to lookup service %s in catalog: %v", serviceID, err)
			continue
		}

		bodyBytes, _ := io.ReadAll(catalogResp.Body)
		catalogResp.Body.Close()

		if catalogResp.StatusCode != http.StatusOK {
			log.Printf("Service %s not found in catalog (status %d)", serviceID, catalogResp.StatusCode)
			continue
		}

		var catalogEntries []struct {
			Node       string `json:"Node"`
			Datacenter string `json:"Datacenter"`
			ServiceID  string `json:"ServiceID"`
		}
		if err := json.Unmarshal(bodyBytes, &catalogEntries); err != nil {
			log.Printf("Failed to decode catalog response for %s: %v", serviceID, err)
			continue
		}

		for _, entry := range catalogEntries {
			deregisterPayload := map[string]string{
				"Node":       entry.Node,
				"Datacenter": entry.Datacenter,
				"ServiceID":  entry.ServiceID,
			}
			payloadBytes, err := json.Marshal(deregisterPayload)
			if err != nil {
				log.Printf("Failed to marshal catalog deregister payload for %s: %v", serviceID, err)
				continue
			}

			catalogDeregisterReq, err := http.NewRequest("PUT", fmt.Sprintf("%s/v1/catalog/deregister", consulAddress), strings.NewReader(string(payloadBytes)))
			if err != nil {
				log.Printf("Failed to create catalog deregister request for %s: %v", serviceID, err)
				continue
			}
			if consulToken != "" {
				catalogDeregisterReq.Header.Set("X-Consul-Token", consulToken)
			}
			catalogDeregisterReq.Header.Set("Content-Type", "application/json")

			catalogDeregisterResp, err := http.DefaultClient.Do(catalogDeregisterReq)
			if err != nil {
				log.Printf("Failed to deregister %s from catalog: %v", serviceID, err)
				continue
			}
			catalogDeregisterResp.Body.Close()
			log.Printf("Deregistered service %s from catalog (status %d)", serviceID, catalogDeregisterResp.StatusCode)
		}
	}

	mu.Lock()
	delete(registeredContainers, shortID)
	mu.Unlock()
	saveState()
}

func saveState() {
	mu.Lock()
	defer mu.Unlock()

	data, err := json.MarshalIndent(registeredContainers, "", "  ")
	if err != nil {
		log.Printf("Failed to marshal state: %v", err)
		return
	}
	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		log.Printf("Failed to write state file: %v", err)
	}
}

func loadState() {
	mu.Lock()
	defer mu.Unlock()

	data, err := os.ReadFile(stateFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Failed to read state file: %v", err)
		}
		return
	}
	if err := json.Unmarshal(data, &registeredContainers); err != nil {
		log.Printf("Failed to unmarshal state: %v", err)
	}
}

func findContainerByName(cli *client.Client, ctx context.Context, name string) (*types.ContainerJSON, error) {
	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return nil, err
	}
	for _, c := range containers {
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == name {
				info, err := cli.ContainerInspect(ctx, c.ID)
				if err != nil {
					return nil, err
				}
				return &info, nil
			}
		}
	}
	return nil, fmt.Errorf("container %s not found", name)
}
