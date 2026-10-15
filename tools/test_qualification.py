import unittest
from qualify_openshift import validate_target
class QualificationGuardTests(unittest.TestCase):
 def test_explicit_disposable_target(self):validate_target('lab','dcmig-test',True)
 def test_no_ambient_or_production_target(self):
  for ctx,ns,ack in [('', 'dcmig-test',True),('lab','production',True),('lab','dcmig-test',False),('lab','dcmig-test;bad',True)]:
   with self.assertRaises(ValueError):validate_target(ctx,ns,ack)
