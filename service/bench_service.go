package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"golang-postgresql/repository"
)

type planNode struct {
	NodeType     string     `json:"Node Type"`
	RelationName string     `json:"Relation Name"`
	IndexName    string     `json:"Index Name"`
	Plans        []planNode `json:"Plans"`
}

type explainOutput struct {
	Plan          planNode `json:"Plan"`
	ExecutionTime float64  `json:"Execution Time"`
}

type benchService struct {
	repo repository.AuditLogRepository
}

func NewBenchService(repo repository.AuditLogRepository) QueryBenchmark {
	return benchService{repo: repo}
}

func (s benchService) ListByActorPlan(ctx context.Context, actorID int64, limit int) (*QueryPlanResult, error) {
	rawJSON, err := s.repo.ExplainListByActor(ctx, actorID, limit)
	if err != nil {
		return nil, err
	}

	return ParsePlan(rawJSON)
}

func ParsePlan(rawJSON string) (*QueryPlanResult, error) {
	var outputs []explainOutput
	if err := json.Unmarshal([]byte(rawJSON), &outputs); err != nil {
		return nil, fmt.Errorf("parse explain output: %w", err)
	}
	if len(outputs) == 0 {
		return nil, fmt.Errorf("parse explain output: empty result")
	}

	output := outputs[0]
	scan, found := findScanNode(output.Plan)
	if !found {
		scan = scanMatch{node: output.Plan}
	}

	summary := fmt.Sprintf("%s, Execution Time: %.2f ms", describeScanNode(scan), output.ExecutionTime)

	return &QueryPlanResult{Summary: summary, RawPlan: rawJSON}, nil
}

type scanMatch struct {
	node      planNode
	indexName string
}

func findScanNode(node planNode) (scanMatch, bool) {
	switch {
	case strings.Contains(node.NodeType, "Bitmap Heap Scan"):
		indexName := ""
		if len(node.Plans) > 0 {
			indexName = node.Plans[0].IndexName
		}
		return scanMatch{node: node, indexName: indexName}, true
	case strings.Contains(node.NodeType, "Seq Scan"), strings.Contains(node.NodeType, "Index Scan"):
		return scanMatch{node: node, indexName: node.IndexName}, true
	}
	for _, child := range node.Plans {
		if found, ok := findScanNode(child); ok {
			return found, true
		}
	}
	return scanMatch{}, false
}

func describeScanNode(s scanMatch) string {
	switch {
	case s.indexName != "":
		return fmt.Sprintf("%s using %s on %s", s.node.NodeType, s.indexName, s.node.RelationName)
	case s.node.RelationName != "":
		return fmt.Sprintf("%s on %s", s.node.NodeType, s.node.RelationName)
	default:
		return s.node.NodeType
	}
}
