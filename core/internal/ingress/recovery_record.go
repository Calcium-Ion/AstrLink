package ingress

import (
	"github.com/QuantumNous/astrlink/core/contract"
)

func (session *recordSession) noteRecoveryStop(reason string) {
	if session == nil {
		return
	}
	if session.pendingAttempt != nil {
		session.privacyRestore = session.pendingAttempt.record.PrivacyRestore
		if session.recovery != nil {
			session.recovery.Action = ""
		}
	}
	if session.recovery == nil {
		session.recovery = &contract.RequestRecovery{}
	}
	session.recovery.StopReason = reason
}

func (session *recordSession) noteRecoveryDecision(index int, schedule *recoverySchedule, reason string) {
	if session == nil {
		return
	}
	if session.recovery == nil {
		session.recovery = &contract.RequestRecovery{}
	}
	session.recovery.Action = "retry"
	if !schedule.sameTarget(schedule.nextIndex, index) {
		session.recovery.Action = "failover"
	}
	session.recovery.Reason = reason
	session.recovery.DelayMS = int(schedule.delay.Milliseconds())
}
