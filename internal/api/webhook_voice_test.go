package api

import (
	"testing"
)

func TestVoiceGroupCleanup_LastParticipantLeave(t *testing.T) {
	t.Skip("Wave 0 stub — implement in M.3-01: verify DeleteMLSGroupInfo called with groupType='voice' when last participant leaves")
}

func TestVoiceGroupCleanup_NotLastParticipant(t *testing.T) {
	t.Skip("Wave 0 stub — implement in M.3-01: verify DeleteMLSGroupInfo NOT called when participants remain")
}

func TestVoiceGroupCleanup_BroadcastVoiceGroupDestroyed(t *testing.T) {
	t.Skip("Wave 0 stub — implement in M.3-01: verify voice_group_destroyed WS event broadcast on last leave")
}
