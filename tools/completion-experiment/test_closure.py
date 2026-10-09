#!/bin/python3
"""Offline source/content/observer fixtures; never run a tool or fake quality success."""
import sys
sys.dont_write_bytecode=True
import tempfile
import unittest
from unittest.mock import patch
import closure
from pathlib import Path
import ast,hashlib,json,os
from closure import (Component,Footprint,Limits,Mutated,Unknown,identity,
                     protected,tree_component,wrapper_reads)

OUT=Path(__file__).resolve().parent
def run_owned_controls(case):
 CASE=Path(case)
 assert CASE.name and not CASE.exists()
 CASE.mkdir()
 root=CASE/'installation';root.mkdir()
 (root/'bin').mkdir();(root/'src').mkdir()
 (root/'bin/tool').write_text('tool bytes, NEVER EXECUTED\n')
 (root/'src/compiler-reader.txt').write_text('first compiler input\n')
 results=[]
 def ok(name,**kw):
  results.append({'control':name,'passed':True,**kw})
  (CASE/'results.json').write_text(json.dumps(results,indent=2)+'\n')
 def discover():return identity((),trees=(root,))

 # A real falsifier for directory/executable-only input identities: descendants
 # change without changing either binding or executable bytes.
 old=discover()
 weak=(root.stat().st_dev,root.stat().st_ino,hashlib.sha256((root/'bin/tool').read_bytes()).hexdigest())
 link=CASE/'already-existing-hardlink';os.link(root/'src/compiler-reader.txt',link)
 link.write_text('changed compiler input\n')
 new=discover()
 assert weak==(root.stat().st_dev,root.stat().st_ino,hashlib.sha256((root/'bin/tool').read_bytes()).hexdigest())
 assert old.digest!=new.digest
 (CASE/'directory-only-falsifier.json').write_text(json.dumps({'oldWeakIdentity':weak,'newWeakIdentity':weak,
  'oldContentIdentity':old.digest,'newContentIdentity':new.digest,'directoryOnlyWouldReuse':True,
  'contentClosureRejects':True},indent=2)+'\n')
 ok('descendant hardlink change defeats sentinel-only identity but not content closure')

 # Provisional identities read before registration are replaced; no claim of
 # detecting a restored event BEFORE registration is made.
 current=discover()
 final,obs=protected(discover,provisional=old)
 assert final.digest==current.digest and final.digest!=old.digest
 ok('preregistration stale identity replaced by protected full content reread',**obs)

 # Under the barrier, a restored mutation is an actual refusal, not equal-endpoint reuse.
 body=link.read_bytes()
 def restored_mutation():link.write_text('transient\n');link.write_bytes(body)
 try:protected(discover,verify=restored_mutation)
 except Mutated:pass
 else:raise AssertionError('restored mutation missed')
 assert discover().digest==current.digest
 ok('post-barrier hardlink restore refuses despite equal final bytes')

 # A later discovery exception cannot downgrade a mutation already pending in the observer.
 def discovery_error_after_mutation():
  link.write_text('mutated while rereading\n')
  raise Unknown('injected unaccounted reader')
 try:protected(discovery_error_after_mutation,provisional=discover())
 except Mutated:pass
 else:raise AssertionError('mutation downgraded to discovery limitation')
 link.write_bytes(body)
 ok('mutation precedence retained when protected discovery also fails')

 # Membership learned after provisional discovery must receive new watches and
 # another complete read, then remain protected through verification and final validation.
 provisional=discover()
 def add_before():
  (root/'new-parent').mkdir();(root/'new-parent/new-reader.txt').write_text('new reader input\n')
 final,obs=protected(discover,provisional=provisional,before_registration=add_before)
 assert str(root/'new-parent/new-reader.txt') in final.members
 assert final.digest!=provisional.digest
 ok('new descriptor membership registered/reread through final validation',**obs)

 # Same endpoint membership with a create/delete event cannot authorize verification.
 probe=root/'transient-member'
 def create_delete():probe.write_text('temp\n');probe.unlink()
 try:protected(discover,verify=create_delete)
 except Mutated:pass
 else:raise AssertionError('create/delete mutation missed')
 ok('create/delete during verification refuses')

 # Ancestor watches protect selected child bindings, not every unrelated output
 # beside them. All children WITHIN an input tree remain watched.
 final,obs=protected(discover,verify=lambda:(CASE/'non-input-output.log').write_text('output outside input tree\n'))
 assert obs['irrelevantAncestorSiblingEvents']>0
 ok('unrelated ancestor sibling output separated from verification inputs',**obs)

 # No manual preparation predicate: absent distribution is a full/capture limitation;
 # automatic identity becomes Known when the actual source-root contents appear.
 # These are file fixtures, not toolchain execution/provisioning/quality successes.
 distribution=CASE/'official-distribution'
 absent=tree_component('official',distribution,'owned artifact byte scope')
 assert absent.state=='Unknown' and 'ordinary full may provision' in absent.limitation
 distribution.mkdir();(distribution/'VERSION').write_text('go1.26.7\n')
 (distribution/'bin').mkdir();(distribution/'bin/go').write_text('NOT AN EXECUTABLE; file identity fixture only\n')
 present=tree_component('official',distribution,'owned artifact byte scope')
 assert present.state=='Known' and absent.footprint.digest!=present.footprint.digest
 ok('absent-to-provisioned component discovery is automatic; no quality outcome claimed')

 # Unsupported external subtree aliases and a bounded root exceedance are Unknown,
 # never silently normalized into a false Known context.
 external=CASE/'external';external.mkdir();(external/'reader').write_text('external\n')
 os.symlink(external,root/'unaccounted-link')
 unsupported=tree_component('unsupported',root,'file fixture')
 assert unsupported.state=='Unknown'
 (root/'unaccounted-link').unlink()
 try:identity((),trees=(root,),limits=Limits(entries=1,bytes=1))
 except Unknown:pass
 else:raise AssertionError('budget silently ignored')
 ok('external-link/type budget limitations remain Unknown')

 # Named GCC transitive seam: literal sources plus their support-data reads and
 # optional hook inputs. The compiler is never invoked.
 wrap=CASE/'wrapper';wrap.mkdir()
 (wrap/'gcc').write_text('#!/bin/sh\nsource '+str(wrap/'add-flags.sh')+'\n'+
  'if [[ -e '+str(wrap/'cc-wrapper-hook')+' ]]; then\n source '+str(wrap/'cc-wrapper-hook')+'\nfi\n')
 (wrap/'add-flags.sh').write_text('if [ -e '+str(wrap/'cc-cflags')+' ]; then\n'+
  ' FLAGS="$(< '+str(wrap/'cc-cflags')+')"\nfi\n')
 (wrap/'cc-cflags').write_text('-isystem /nix/store/'+('a'*32)+'-fixture-libc/include\n')
 component,graph=wrapper_reads(wrap/'gcc')
 assert component.state=='Known' and str(wrap/'cc-cflags') in component.footprint.members
 assert str(wrap/'cc-wrapper-hook') in graph['guardedAbsentInputs']
 main_sha=hashlib.sha256((wrap/'gcc').read_bytes()).hexdigest()
 (wrap/'cc-cflags').write_text('-isystem /nix/store/'+('b'*32)+'-another-libc/include\n')
 changed,changed_graph=wrapper_reads(wrap/'gcc')
 assert main_sha==hashlib.sha256((wrap/'gcc').read_bytes()).hexdigest()
 assert changed.footprint.digest!=component.footprint.digest
 assert graph['furtherToolAndIncludeRoots']!=changed_graph['furtherToolAndIncludeRoots']
 ok('wrapper unchanged but sourced flag bytes/path membership changes invalidate component')
 provisional=changed.footprint
 (wrap/'cc-wrapper-hook').write_text('# new hook read input\n')
 expanded,_=wrapper_reads(wrap/'gcc')
 assert expanded.footprint.digest!=provisional.digest
 final,obs=protected(lambda:wrapper_reads(wrap/'gcc')[0].footprint,provisional=provisional)
 assert final.digest==expanded.footprint.digest
 ok('absent wrapper hook addition expands automatic observed membership',**obs)
 (wrap/'add-flags.sh').write_text('source "$CUSTOM_SOURCE"\n')
 try:wrapper_reads(wrap/'gcc')
 except Unknown:pass
 else:raise AssertionError('dynamic source pretended closed')
 ok('new opaque dynamic wrapper source returns Unknown')


 # The five follow-up cases exercise the same source module and only owned fixtures.
 for narrow in run(closure, CASE/'narrow'):
  assert narrow['passed'],narrow
  ok(narrow['control'],**{k:v for k,v in narrow.items() if k not in ('control','passed')})
 return results

def run(module, case):
    case = Path(case)
    case.mkdir(exist_ok=False)
    results = []

    def record(control, passed, **evidence):
        results.append(dict(control=control, passed=bool(passed), **evidence))
        (case / 'results.json').write_text(json.dumps(results, indent=2) + '\n')

    # Descendant lstat succeeds, but its scandir fails deterministically even as root.
    root = case / 'enumeration'; root.mkdir()
    descendant = root / 'descendant'; descendant.mkdir()
    payload = descendant / 'payload'; payload.write_bytes(b'owned input\n')
    real_scandir = os.scandir
    calls = []

    def failing_scandir(path):
        if Path(path) == descendant:
            calls.append(str(path))
            raise PermissionError(13, 'owned injected descendant scandir failure', str(path))
        return real_scandir(path)

    stat_succeeded = descendant.lstat().st_ino > 0
    with patch.object(os, 'scandir', failing_scandir):
        incomplete = module.tree_component('enumeration', root, 'owned complete-tree byte scope')
    restored = module.tree_component('enumeration', root, 'owned complete-tree byte scope')
    record('descendant enumeration failure is Unknown; restoration recovers full membership',
           stat_succeeded and bool(calls) and incomplete.state == 'Unknown' and
           restored.state == 'Known' and str(payload) in restored.footprint.members and
           restored.footprint.files == 1,
           descendantStatSucceeded=stat_succeeded, injectedScandirCalls=calls,
           failedComponent=incomplete.report(), restoredComponent=restored.report(),
           payloadPresentInFailedFootprint=str(payload) in incomplete.footprint.members,
           payloadPresentAfterRestoration=str(payload) in restored.footprint.members)

    # Decode a real restored write event before surfacing the enumeration failure.
    # The observer retains the decoded events; availability cannot override refusal.
    holder = {}
    original_monitor = module.Monitor

    class RecordingMonitor(original_monitor):
        def __init__(self):
            super().__init__()
            holder['monitor'] = self

    original = payload.read_bytes()
    decoded = []

    def failing_after_decoded_mutation(path):
        if Path(path) == descendant:
            payload.write_bytes(b'transient owned mutation\n')
            payload.write_bytes(original)
            try:
                holder['monitor'].drain()
            except module.Mutated:
                decoded.extend(holder['monitor'].events)
            assert decoded, 'fixture must decode actual events before discovery failure'
            raise PermissionError(13, 'enumeration failed after decoded mutation', str(path))
        return real_scandir(path)

    provisional = module.identity((), trees=(root,))
    observed_state = 'returned'
    observation_error = ''
    discovery_failure = {}

    def protected_discovery():
        component = module.tree_component('enumeration', root, 'owned complete-tree byte scope')
        discovery_failure['componentState'] = component.state
        discovery_failure['limitation'] = component.limitation
        if component.state == 'Unknown':
            raise module.Unknown(component.limitation)
        return component.footprint

    with patch.object(module, 'Monitor', RecordingMonitor), patch.object(os, 'scandir', failing_after_decoded_mutation):
        try:
            module.protected(protected_discovery, provisional=provisional)
        except module.Mutated as exc:
            observed_state = 'refused-Mutated'; observation_error = str(exc)
        except (module.Unknown, module.Unavailable, OSError) as exc:
            observed_state = type(exc).__name__; observation_error = str(exc)
    record('already decoded mutation outranks protected enumeration/discovery failure',
           observed_state == 'refused-Mutated' and bool(decoded) and payload.read_bytes() == original,
           outcome=observed_state, decodedEvents=decoded, discovery=discovery_failure,
           restoredBytesEqual=True, error=observation_error)

    # The controlled read seam grows only owned files after the real lstat has
    # returned. Each read request/actual return is recorded. Monitor creation is
    # forbidden: the byte bound must hold in provisional discovery itself.
    def growth_test(name, limit, prefix_bytes, grown_bytes):
        directory = case / name; directory.mkdir()
        paths = []
        if prefix_bytes:
            prefix = directory / 'prefix'; prefix.write_bytes(b'p' * prefix_bytes); paths.append(prefix)
        growing = directory / 'growing'; growing.write_bytes(b'g'); paths.append(growing)
        initial_size = growing.lstat().st_size
        real_open = Path.open
        reads = []
        grown = False
        monitor_calls = []

        class Reader:
            def __init__(self, file, path): self.file = file; self.path = path
            def __enter__(self): return self
            def __exit__(self, *args): self.file.close()
            def read(self, size):
                chunk = self.file.read(size)
                reads.append(dict(path=str(self.path), requested=size, consumed=len(chunk)))
                return chunk

        def controlled_open(path, mode='r', *args, **kwargs):
            nonlocal grown
            if mode == 'rb' and path in paths:
                if path == growing and not grown:
                    with real_open(path, 'ab') as writer: writer.write(b'g' * (grown_bytes - 1))
                    grown = True
                return Reader(real_open(path, mode, *args, **kwargs), path)
            return real_open(path, mode, *args, **kwargs)

        def forbidden_monitor():
            monitor_calls.append(True)
            raise AssertionError('narrow budget fixture must not construct a monitor')

        state = 'returned'; footprint = None; error = ''
        with patch.object(Path, 'open', controlled_open), patch.object(module, 'Monitor', forbidden_monitor):
            try:
                footprint = module.identity(paths, limits=module.Limits(entries=64, bytes=limit))
            except module.Unknown as exc:
                state = 'Unknown'; error = str(exc)
        consumed = sum(row['consumed'] for row in reads)
        cumulative = 0; bounded_requests = True
        for row in reads:
            bounded_requests &= 0 < row['requested'] <= min(1024 * 1024, limit - cumulative + 1)
            cumulative += row['consumed']
        evidence = dict(limit=limit, initialSize=initial_size, grownSize=grown_bytes,
                        bytesConsumed=consumed, readCalls=reads, boundedRequests=bool(bounded_requests),
                        monitorConstructed=bool(monitor_calls), outcome=state, error=error,
                        footprint=footprint.report() if footprint else None)
        if prefix_bytes + grown_bytes > limit:
            passed = (state == 'Unknown' and grown and initial_size == 1 and
                      not monitor_calls and bounded_requests and consumed == limit + 1 and footprint is None)
        else:
            actual_length = None
            if footprint is not None:
                value = footprint.members[str(growing.absolute())]
                actual_length = ast.literal_eval(value.removeprefix('file:').rsplit(':', 1)[0])[3]
            evidence['memberContentLength'] = actual_length
            passed = (state == 'returned' and grown and not monitor_calls and bounded_requests and
                      footprint.bytes == consumed == prefix_bytes + grown_bytes and actual_length == grown_bytes)
        record(name, passed, **evidence)

    growth_test('stale size growth stops at remaining budget plus one before monitor', 4, 0, 64)
    growth_test('actual byte budget is cumulative across files', 6, 3, 64)
    growth_test('within budget growth reports actual hashed bytes and content length', 64, 3, 32)
    (case / 'source.json').write_text(json.dumps({'source':str(Path(module.__file__).resolve()),
        'sha256':hashlib.sha256(Path(module.__file__).read_bytes()).hexdigest(),
        'actualRootScans':0,'toolCommandsRun':[]}, indent=2) + '\n')
    return results


class ClosureFixtureTests(unittest.TestCase):
 def test_owned_closure_controls(self):
  with tempfile.TemporaryDirectory(prefix='completion-closure-', dir=OUT) as temp:
   results=run_owned_controls(Path(temp)/'case')
  failures=[item['control'] for item in results if not item['passed']]
  self.assertEqual(len(results),17)
  self.assertEqual(failures,[])
