import copy,unittest
from release_gate import verify,SCENARIOS
class GateTests(unittest.TestCase):
 def setUp(self):
  self.matrix={'newestGA':'4.22','targets':[{'minor':x,'releaseRequirement':'required'} for x in ['4.14','4.22']]}
  self.rows=[dict(kind='actual-openshift',minor=x,artifactSHA256='sha',result='passed',revision='rev',fixtureSHA256='fixtures',dependencySHA256='deps',openshiftPatch=x+'.1',kubernetesVersion='v1.27.16' if x=='4.14' else 'v1.35.1',dependencyVersions={k:'v0.31.3' for k in ['k8s.io/api','k8s.io/apimachinery','k8s.io/client-go']},releaseImage='image@sha256:123',scenarios=list(SCENARIOS),runner='runner',runURL='https://ci.example/run',testedAt='2026-10-15T00:00:00Z') for x in ['4.14','4.22']]
 def check(self,rows):return verify(self.matrix,rows,{'linux-amd64':'sha'},'rev','fixtures','deps')
 def test_valid_contract(self):self.assertTrue(self.check(self.rows))
 def test_missing_and_simulated(self):
  for rows in [[],[dict(r,kind='simulated') for r in self.rows]]:
   with self.assertRaises(ValueError):self.check(rows)
 def test_stale_or_incomplete(self):
  for field,value in [('revision','old'),('dependencySHA256','old'),('scenarios',['schema']),('result','skipped'),('openshiftPatch',''),('kubernetesVersion','v1.28.0')]:
   with self.subTest(field=field):
    rows=copy.deepcopy(self.rows);rows[0][field]=value
    with self.assertRaises(ValueError):self.check(rows)
