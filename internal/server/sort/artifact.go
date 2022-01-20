package sort

import (
	"sort"

	"github.com/golang/protobuf/ptypes"

	pb "github.com/hashicorp/waypoint/pkg/server/gen"
)

// ArtifactStartDesc sorts builds by start time descending (most recent first).
// For the opposite, use sort.Reverse.
type ArtifactStartDesc []*pb.PushedArtifact

func (s ArtifactStartDesc) Len() int      { return len(s) }
func (s ArtifactStartDesc) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s ArtifactStartDesc) Less(i, j int) bool {
	t1, err := ptypes.Timestamp(s[i].Status.StartTime)
	if err != nil {
		return false
	}

	t2, err := ptypes.Timestamp(s[j].Status.StartTime)
	if err != nil {
		return false
	}

	return t2.Before(t1)
}

var (
	_ sort.Interface = (ArtifactStartDesc)(nil)
)
