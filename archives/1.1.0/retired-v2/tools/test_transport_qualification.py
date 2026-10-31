import unittest
from transport_qualification import qualification_ok,REQUIRED
class QualificationEvidenceTests(unittest.TestCase):
    def summary(self,**kw):return dict(passed=[REQUIRED],failed=[],missing_required=[],skipped=[])|kw
    def test_failure_cannot_pass_with_empty_skip_or_stale_source(self):
        for code,summary,before,after in [(1,self.summary(),'a','a'),(0,self.summary(passed=[]),'a','a'),(0,self.summary(skipped=[REQUIRED]),'a','a'),(0,self.summary(missing_required=[REQUIRED]),'a','a'),(0,self.summary(failed=[REQUIRED]),'a','a'),(0,self.summary(),'a','b')]:self.assertFalse(qualification_ok(code,summary,before,after))
        self.assertTrue(qualification_ok(0,self.summary(),'a','a'))
if __name__=='__main__':unittest.main()
