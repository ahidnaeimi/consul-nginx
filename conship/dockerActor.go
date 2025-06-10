package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/docker/docker/client"
	"github.com/docker/docker/api/types/container"
)

func getContainerInfo(cli *client.Client, containerID string) (ServiceInfo, error) {
	ctx := context.Background()

	jsonData, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		log.Printf("Failed to inspect container %s: %v", containerID, err)
		return ServiceInfo{}, err
	}

	// فقط اگر لیبل SERVICE_NAME وجود دارد، ثبت شود
	svcName, ok := jsonData.Config.Labels["SERVICE_NAME"]
	if !ok || svcName == "" {
		log.Printf("SERVICE_NAME label missing for container %s", containerID[:12])
		return ServiceInfo{}, fmt.Errorf("SERVICE_NAME label missing for container %s", containerID[:12])
	}

	conshipContainer, err := findContainerByName(cli, ctx, containerName)
	if err != nil {
		log.Printf("Failed to find conship container: %v", err)
		return ServiceInfo{}, err
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
		return ServiceInfo{}, fmt.Errorf("Conship container has no IP")
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
		return ServiceInfo{}, fmt.Errorf("Container %s is not on the same network as conship", jsonData.Name)
	}

	containerName := strings.TrimPrefix(jsonData.Name, "/")
	serviceAddress := jsonData.NetworkSettings.Networks[sharedNet].IPAddress

	// var ports []int
	var allowedPorts map[int]bool
	portsLabel, ok := jsonData.Config.Labels["SERVICE_PORTS"]
	if ok && portsLabel != "" {
		allowedPorts = make(map[int]bool)
		for _, part := range strings.Split(portsLabel, ",") {
			p, err := strconv.Atoi(strings.TrimSpace(part))
			if err != nil {
				log.Printf("Invalid port in SERVICE_PORTS label: %v", err)
				continue
			}
			allowedPorts[p] = true
		}
	}

	tags := []string{"docker"}
	envTags := ""
	for _, env := range jsonData.Config.Env {
		if strings.HasPrefix(env, "SERVICE_TAGS=") {
			envTags = strings.TrimPrefix(env, "SERVICE_TAGS=")
			break
		}
	}
	if envTags != "" {
		for _, tag := range strings.Split(envTags, ",") {
			if trimmed := strings.TrimSpace(tag); trimmed != "" {
				tags = append(tags, trimmed)
			}
		}
	}

	return ServiceInfo{
		SvcName:        svcName,
		ContainerName:  containerName,
		ContainerID:    containerID,
		ServiceAddress: serviceAddress,
		AllowedPorts:   allowedPorts,
		Tags:           tags,
	}, nil
}

func removeContainer(cli *client.Client, prevServiceInfo ServiceInfo) error {
	ctx := context.Background()

	err := cli.ContainerRemove(ctx, prevServiceInfo.ContainerID, container.RemoveOptions{
		Force: true, // اگه در حال اجرا باشه، kill می‌کنه
	})
	if err != nil {
		return fmt.Errorf("failed to remove container %s: %w", prevServiceInfo.ContainerID, err)
	}

	fmt.Printf("✅ Container %s removed\n", prevServiceInfo.ContainerID)
	return nil
}