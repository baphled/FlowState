import copy
import importlib.util
import unittest
from pathlib import Path


spec = importlib.util.spec_from_file_location('agent_work', Path(__file__).resolve().parents[1] / 'agent_work.py')
agent_work = importlib.util.module_from_spec(spec)
spec.loader.exec_module(agent_work)


class AgentWorkTests(unittest.TestCase):
    def setUp(self):
        self.policy = {'policy_version':'flowstate-agent-roles-v1','max_active_work_items_per_agent':1,
                       'roles':{'senior':{'work_kinds':['complex'],'delegate_to':['mid','junior']},
                                'mid':{'work_kinds':['bounded'],'delegate_to':[]},
                                'junior':{'work_kinds':['localized'],'delegate_to':[]},
                                'verifier':{'work_kinds':['review'],'delegate_to':[]}}}
        self.plan = {'assignments':[{'work_item_id':'design','agent_id':'senior','role':'senior','work_kind':'complex','scope_ids':['provider']},
                                    {'work_item_id':'adapter','agent_id':'mid','role':'mid','work_kind':'bounded','scope_ids':['provider']},
                                    {'work_item_id':'tests','agent_id':'junior','role':'junior','work_kind':'localized','scope_ids':['provider']},
                                    {'work_item_id':'review','agent_id':'verifier','role':'verifier','work_kind':'review','scope_ids':['provider']}],
                     'delegations':[{'parent_agent_id':'senior','child_agent_id':'mid'},{'parent_agent_id':'senior','child_agent_id':'junior'}],
                     'active_leases':[], 'reviews':[{'author_agent_id':'mid','reviewer_agent_id':'verifier'}],
                     'budget':{'max_tokens':100,'spent_tokens':10,'reserved_tokens':50,'max_minutes':30,'spent_minutes':5,'max_attempts':3,'spent_attempts':1}}

    def test_senior_delegates_bounded_work_and_independent_review(self):
        self.assertEqual(agent_work.plan_errors(self.plan, self.policy), [])

    def test_rejects_four_capability_batch_on_one_engineer(self):
        self.plan['assignments'][0]['scope_ids'] = ['M1-PROVIDER','M1-EVENTS','M1-SESSION','M1-TOOLS']
        self.assertTrue(agent_work.plan_errors(self.plan, self.policy))

    def test_rejects_multiple_active_work_items_for_one_agent(self):
        self.plan['active_leases'] = [{'agent_id':'mid','work_item_id':'other'}]
        self.assertTrue(agent_work.plan_errors(self.plan, self.policy))
        self.plan['active_leases'] = []
        self.plan['assignments'].append(copy.deepcopy(self.plan['assignments'][1]))
        self.assertTrue(agent_work.plan_errors(self.plan, self.policy))

    def test_senior_cannot_take_routine_work_and_junior_cannot_delegate(self):
        self.plan['assignments'][0]['work_kind'] = 'localized'
        self.assertTrue(agent_work.plan_errors(self.plan, self.policy))
        self.plan['assignments'][0]['work_kind'] = 'complex'
        self.plan['delegations'] = [{'parent_agent_id':'junior','child_agent_id':'mid'}]
        self.assertTrue(agent_work.plan_errors(self.plan, self.policy))

    def test_senior_child_cannot_escape_to_another_capability(self):
        self.plan['assignments'][1]['scope_ids'] = ['other-capability']
        self.assertTrue(agent_work.plan_errors(self.plan, self.policy))

    def test_exhausted_tokens_rejected_fully_reserved_capacity_valid(self):
        self.plan['budget'].update(spent_tokens=100, reserved_tokens=0)
        self.assertTrue(agent_work.plan_errors(self.plan, self.policy))
        self.plan['budget'].update(spent_tokens=10, reserved_tokens=90)
        self.assertEqual(agent_work.plan_errors(self.plan, self.policy), [])

    def test_existing_work_item_owner_cannot_be_reassigned_without_transfer(self):
        self.plan['active_leases'] = [{'agent_id':'existing-writer','work_item_id':'adapter'}]
        self.assertTrue(agent_work.plan_errors(self.plan, self.policy))
        self.plan['active_leases'] = [{'agent_id':'mid','work_item_id':'adapter'}]
        self.assertEqual(agent_work.plan_errors(self.plan, self.policy), [])

    def test_verifier_scope_must_match_author_scope(self):
        self.plan['assignments'][3]['scope_ids'] = ['other-capability']
        self.assertTrue(agent_work.plan_errors(self.plan, self.policy))

    def test_actor_and_work_item_ids_must_be_nonempty_strings(self):
        self.plan['assignments'][0]['agent_id'] = 123
        self.assertTrue(agent_work.plan_errors(self.plan, self.policy))

    def test_rejects_budget_over_reservation_and_self_review(self):
        self.plan['budget']['reserved_tokens'] = 100
        self.assertTrue(agent_work.plan_errors(self.plan, self.policy))
        self.plan['budget']['reserved_tokens'] = 50
        self.plan['reviews'] = [{'author_agent_id':'mid','reviewer_agent_id':'mid'}]
        self.assertTrue(agent_work.plan_errors(self.plan, self.policy))


if __name__ == '__main__':
    unittest.main()
