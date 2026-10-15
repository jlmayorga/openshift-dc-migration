#!/usr/bin/env python3
"""Validate actual cluster evidence for every shipped binary and released minor."""
import argparse,datetime,hashlib,json,pathlib,subprocess,sys
SCENARIOS={'schema','dry-run','admission-scc','service-routing','image-triggers','cutover-exclusive','cutover-overlap','rollback','failed-rollout','interruption','stale-plan','safe-rerun','hpa-retarget'}
def digest(path):return hashlib.sha256(pathlib.Path(path).read_bytes()).hexdigest()
def verify(matrix,evidence,artifacts,revision,fixture_hash,dependency_hash):
    required={v['minor'] for v in matrix['targets'] if v['releaseRequirement']!='development'}
    if '4.14' not in required or matrix['newestGA'] not in required:raise ValueError('Missing floor or newest GA target')
    rows={}
    for item in evidence:
        if item.get('kind')!='actual-openshift':continue
        key=(item.get('minor'),item.get('artifactSHA256'))
        if key in rows:raise ValueError('Ambiguous evidence for '+str(key))
        rows[key]=item
    for minor in required:
        for artifact,sha in artifacts.items():
            row=rows.get((minor,sha),{})
            if row.get('result')!='passed':raise ValueError('Missing passing actual-cluster evidence: '+minor+' '+artifact)
            if row.get('revision')!=revision or row.get('fixtureSHA256')!=fixture_hash or row.get('dependencySHA256')!=dependency_hash:raise ValueError('Stale evidence: '+minor+' '+artifact)
            if not row.get('openshiftPatch','').startswith(minor+'.') or not row.get('kubernetesVersion') or not row.get('releaseImage'):raise ValueError('Missing exact release metadata')
            if minor=='4.14' and not row['kubernetesVersion'].startswith('v1.27.'):raise ValueError('Floor must use actual Kubernetes 1.27')
            if not SCENARIOS.issubset(set(row.get('scenarios',[]))):raise ValueError('Incomplete scenarios: '+minor)
            if not row.get('runner') or not row.get('runURL') or not row.get('testedAt'):raise ValueError('Missing provenance')
            datetime.datetime.fromisoformat(row['testedAt'].replace('Z','+00:00'))
    return True

def tree_digest(root):
    h=hashlib.sha256()
    for p in sorted(pathlib.Path(root).rglob('*')):
        if p.is_file():h.update(p.relative_to(root).as_posix().encode()+b'\0'+p.read_bytes())
    return h.hexdigest()
def main():
    p=argparse.ArgumentParser();p.add_argument('--artifacts',required=True);p.add_argument('--evidence',default='compatibility/results');a=p.parse_args()
    matrix=json.loads(pathlib.Path('compatibility/matrix.json').read_text())
    evidence=[json.loads(p.read_text()) for p in pathlib.Path(a.evidence).glob('*.json')]
    artifacts={p.name:digest(p) for p in pathlib.Path(a.artifacts).glob('openshift-dc-converter-*') if p.is_file()}
    if not artifacts:raise ValueError('No release binaries')
    revision=subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()
    verify(matrix,evidence,artifacts,revision,tree_digest('testdata'),digest('go.mod'))
    print('All required binary/version qualification rows passed')
if __name__=='__main__':
    try:main()
    except (ValueError,KeyError) as exc:sys.exit(str(exc))
