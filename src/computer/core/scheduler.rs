use std::collections::{BTreeMap, VecDeque};

use time::OffsetDateTime;

use crate::ids::{AgentId, RunId};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) enum WorkStrength {
    Hard,
    Ambient,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(in crate::computer) struct RunPriority {
    pub(in crate::computer) explicit_human_redirect: bool,
    pub(in crate::computer) strength: WorkStrength,
    pub(in crate::computer) available_at: OffsetDateTime,
    pub(in crate::computer) has_task_continuity: bool,
}

#[derive(Clone, Debug, Eq, PartialEq)]
pub(in crate::computer) struct PendingRun {
    pub(in crate::computer) run_id: RunId,
    pub(in crate::computer) agent_id: AgentId,
    pub(in crate::computer) explicit_human_redirect: bool,
    pub(in crate::computer) strength: WorkStrength,
    pub(in crate::computer) available_at: OffsetDateTime,
    pub(in crate::computer) has_task_continuity: bool,
}

#[derive(Debug)]
pub(in crate::computer) struct Scheduler {
    capacity: usize,
    active_agents: BTreeMap<AgentId, RunId>,
    queues: BTreeMap<AgentId, Vec<PendingRun>>,
    round_robin: VecDeque<AgentId>,
}

impl Scheduler {
    pub(in crate::computer) fn new(capacity: usize) -> Self {
        Self {
            capacity,
            active_agents: BTreeMap::new(),
            queues: BTreeMap::new(),
            round_robin: VecDeque::new(),
        }
    }

    pub(in crate::computer) fn enqueue(&mut self, pending: PendingRun) {
        if self
            .active_agents
            .get(&pending.agent_id)
            .is_some_and(|run_id| *run_id == pending.run_id)
            || self
                .queues
                .values()
                .flatten()
                .any(|queued| queued.run_id == pending.run_id)
        {
            return;
        }
        let agent_id = pending.agent_id;
        let queue = self.queues.entry(agent_id).or_default();
        queue.push(pending);
        queue.sort_by_key(priority_key);
        if !self.round_robin.contains(&agent_id) {
            self.round_robin.push_back(agent_id);
        }
    }

    pub(in crate::computer) fn occupy(&mut self, agent_id: AgentId, run_id: RunId) {
        self.active_agents.insert(agent_id, run_id);
    }

    pub(in crate::computer) fn next(&mut self) -> Option<PendingRun> {
        if self.active_agents.len() >= self.capacity {
            return None;
        }
        let attempts = self.round_robin.len();
        for _ in 0..attempts {
            let agent_id = self.round_robin.pop_front()?;
            if self.active_agents.contains_key(&agent_id) {
                self.round_robin.push_back(agent_id);
                continue;
            }
            let Some(queue) = self.queues.get_mut(&agent_id) else {
                continue;
            };
            let pending = queue.remove(0);
            self.active_agents.insert(agent_id, pending.run_id);
            if queue.is_empty() {
                self.queues.remove(&agent_id);
            } else {
                self.round_robin.push_back(agent_id);
            }
            return Some(pending);
        }
        None
    }

    pub(in crate::computer) fn release(&mut self, run_id: RunId) {
        self.active_agents.retain(|_, active| *active != run_id);
    }
}

fn priority_key(pending: &PendingRun) -> (bool, bool, OffsetDateTime, bool) {
    (
        !pending.explicit_human_redirect,
        pending.strength != WorkStrength::Hard,
        pending.available_at,
        !pending.has_task_continuity,
    )
}
