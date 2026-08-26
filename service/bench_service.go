package service

import (
	"context"
	"encoding/json"
	"fmt"

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
		scan = output.Plan
	}

	summary := fmt.Sprintf("%s, Execution Time: %.2f ms", describeScanNode(scan), output.ExecutionTime)

	return &QueryPlanResult{Summary: summary, RawPlan: rawJSON}, nil
}

func findScanNode(node planNode) (planNode, bool) {
	if node.RelationName != "" {
		return withIndexName(node), true
	}
	for _, child := range node.Plans {
		if found, ok := findScanNode(child); ok {
			return found, true
		}
	}
	return planNode{}, false
}

func withIndexName(node planNode) planNode {
	if node.IndexName != "" {
		return node
	}
	for _, child := range node.Plans {
		if child.IndexName != "" {
			node.IndexName = child.IndexName
			return node
		}
	}
	return node
}

func describeScanNode(node planNode) string {
	switch {
	case node.IndexName != "":
		return fmt.Sprintf("%s using %s on %s", node.NodeType, node.IndexName, node.RelationName)
	case node.RelationName != "":
		return fmt.Sprintf("%s on %s", node.NodeType, node.RelationName)
	default:
		return node.NodeType
	}
}
