package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	protoServicePattern = regexp.MustCompile(`\bservice\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{`)
	protoRPCPattern     = regexp.MustCompile(`\brpc\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
)

func (a *analyzer) collectProtoServices(paths []string) error {
	for _, path := range paths {
		source, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		for _, service := range protoServices(string(source)) {
			methods := a.java.services[service.name]
			if methods == nil {
				methods = make(map[string]bool)
				a.java.services[service.name] = methods
			}
			for _, method := range service.methods {
				methods[lowerCamel(method)] = true
			}
		}
	}
	return nil
}

type protoService struct {
	name    string
	methods []string
}

func protoServices(source string) []protoService {
	var services []protoService
	for _, match := range protoServicePattern.FindAllStringSubmatchIndex(source, -1) {
		bodyStart := match[1]
		bodyEnd := matchingBrace(source, bodyStart-1)
		if bodyEnd < 0 {
			continue
		}
		service := protoService{name: source[match[2]:match[3]]}
		for _, rpc := range protoRPCPattern.FindAllStringSubmatch(source[bodyStart:bodyEnd], -1) {
			service.methods = append(service.methods, rpc[1])
		}
		services = append(services, service)
	}
	return services
}

func matchingBrace(source string, opening int) int {
	depth := 0
	for index := opening; index < len(source); index++ {
		switch source[index] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

func lowerCamel(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToLower(value[:1]) + value[1:]
}
