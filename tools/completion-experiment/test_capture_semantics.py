#!/bin/python3
"""Owned source-equivalence falsifiers; no Go argv, installation scan or secrets."""
import sys
sys.dont_write_bytecode=True
from pathlib import Path
import hashlib,json,os,tempfile,unittest
import capture
OUT=Path(__file__).resolve().parent
PACKET=OUT


def run(c,case,source=None):
    case=Path(case);case.mkdir(exist_ok=False);results=[]
    def record(control,passed,**evidence):
        results.append(dict(control=control,passed=bool(passed),**evidence))
        (case/'results.json').write_text(json.dumps(results,indent=2)+'\n')
    root=case/'owned';root.mkdir()
    actual=root/'actual';other=root/'other';default=root/'default';override=root/'override'
    for p in (actual,other,default,override):p.mkdir();(p/'input.h').write_text('owned descendant\n')
    goroot=root/'goroot';goroot.mkdir();defaults=goroot/'go.env';settings=root/'goenv'
    defaults.write_text('CGO_CFLAGS=-O2 -g\n')
    values={'GOROOT':str(goroot),'GOENV':str(settings)}
    effective=lambda env={}:c.FullCapture.settings(None,values,env)
    def describe():return c.flag_descriptor([],list(effective().values()))

    settings.write_bytes(('CGO_CPPFLAGS=-I'+str(actual)+'\n CGO_CPPFLAGS=-I'+str(other)+'\n').encode())
    before=describe();selected=effective()['CGO_CPPFLAGS'];payload=actual/'input.h'
    body=payload.read_bytes();payload.write_text('changed actual input\n');after=describe();payload.write_bytes(body)
    member=str(payload) in before.footprint.members
    record('untrimmed GOENV ignores indented second line and actual descendant invalidates',
        before.state=='Known' and selected=='-I'+str(actual) and member and str(other) not in before.roots and before.footprint.digest!=after.footprint.digest,
        state=before.state,selectedActual=selected=='-I'+str(actual),actualRoot=str(actual),otherRoot=str(other),
        actualDescendantPresent=member,descendantChangeInvalidates=before.footprint.digest!=after.footprint.digest,
        beforeIdentity=before.footprint.report(),afterIdentity=after.footprint.report())

    # LF is the ONLY line separator. Whitespace/CR/key spelling are untrimmed.
    settings.write_bytes(b' #ignored=X\n\tCGO_CFLAGS=ignored\nCGO_CFLAGS= -O2 -g \r\n'+
        'OTHER=one\u2028CGO_CPPFLAGS=not-a-new-line\u0085tail\n'.encode()+
        b'CGO_CPPFLAGS =not-the-same-key\n=ignored\nlowercase=ignored\nFINAL=value\vretained\fretained')
    expected={'CGO_CFLAGS':' -O2 -g \r','OTHER':'one\u2028CGO_CPPFLAGS=not-a-new-line\u0085tail',
        'CGO_CPPFLAGS ':'not-the-same-key','FINAL':'value\vretained\fretained'}
    record('LF-only settings preserve CR Unicode separators values and exact uppercase-first key semantics',
        c.read_go_settings(settings)==expected,expectedEntries=len(expected),observedEntries=len(c.read_go_settings(settings)))

    defaults.write_text('CGO_CPPFLAGS=-I'+str(default)+'\nCGO_CFLAGS=-Droot\n')
    settings.write_text('CGO_CPPFLAGS=-I'+str(actual)+'\nCGO_CFLAGS=\n')
    base=effective();empty=effective({'CGO_CPPFLAGS':''});nonempty=effective({'CGO_CPPFLAGS':'-I'+str(override)})
    disabled=c.FullCapture.settings(None,dict(values,GOENV='off'),{})
    settings.unlink();absent=effective()
    record('nonempty environment precedence user empty-value override and GOROOT defaults remain source-equivalent',
        base['CGO_CPPFLAGS']==empty['CGO_CPPFLAGS']=='-I'+str(actual) and nonempty['CGO_CPPFLAGS']=='-I'+str(override) and
        base['CGO_CFLAGS']=='-O2 -g' and disabled['CGO_CPPFLAGS']==absent['CGO_CPPFLAGS']=='-I'+str(default),
        environmentNonemptyWins=nonempty['CGO_CPPFLAGS']=='-I'+str(override),rootDefaultsPreserved=disabled==absent)
    defaults.write_text('CGO_CFLAGS=-O2 -g\n')

    actual_backslash=root/'a\\b';shell_other=root/'ab'
    for p in (actual_backslash,shell_other):p.mkdir();(p/'input.h').write_text('owned backslash reader\n')
    flag='-I'+str(actual_backslash)
    first=c.flag_descriptor([],[flag]);file=actual_backslash/'input.h';old=file.read_bytes()
    file.write_text('changed actual backslash input\n');second=c.flag_descriptor([],[flag]);file.write_bytes(old)
    record('Go CGO literal Linux backslash root is exact and actual descendant invalidates',
        first.state=='Known' and str(actual_backslash) in first.roots and str(shell_other) not in first.roots and
        str(file) in first.footprint.members and first.footprint.digest!=second.footprint.digest,
        actualRoot=str(actual_backslash),shellOtherRoot=str(shell_other),state=first.state,
        actualDescendantPresent=str(file) in first.footprint.members,
        descendantChangeInvalidates=first.footprint.digest!=second.footprint.digest,
        beforeIdentity=first.footprint.report(),afterIdentity=second.footprint.report())

    # These are synthetic token strings, not actual user flags/credentials.
    examples=[('a\\b',['a\\b']),('"a\\b"',['a\\b']),('left"mid',[ 'left"mid']),
        ("'one two'\"three four\"",['one two','three four']),(' \tfirst\rsecond\nthird',['first','second','third']),
        ('a\vb\fc',['a\vb\fc']),('a\u0085b\u2028c',['a\u0085b\u2028c']),
        ('"" \' a \' tail',['',' a ','tail']),('"a\\"b"',['a\\','b"']),
        ('"first"second',['first','second'])]
    parser=getattr(c,'go_quoted_split',None)
    matches=[]
    if parser:
        for text,tokens in examples:
            try:matches.append(parser(text)==tokens)
            except c.Unknown:matches.append(False)
    record('Go quoted Split token matrix uses field-start quotes ASCII whitespace and no unescaping',
        parser is not None and len(matches)==len(examples) and all(matches),cases=len(examples),casesMatched=sum(matches),parserPresent=parser is not None)

    spaced=root/'spaced root';embedded=root/'embedded"quote'
    for p in (spaced,embedded):p.mkdir();(p/'input.h').write_text('owned quote path\n')
    quote_cases=[("'-I"+str(spaced)+"'",spaced),('-I "'+str(spaced)+'"',spaced),
        ('-I'+str(embedded),embedded),("'-I"+str(actual)+"'-Dextra",actual)]
    matched=[]
    for text,path in quote_cases:
        descriptor=c.flag_descriptor([],[text])
        matched.append(descriptor.state=='Known' and str(path) in descriptor.roots and str(path/'input.h') in descriptor.footprint.members)
    record('leading and embedded quotes select exact Go read roots including adjacent fields',all(matched),cases=len(matched),casesMatched=sum(matched))

    unclosed=c.flag_descriptor([],['-I "'+str(actual)])
    empty_path=c.flag_descriptor([],['-I ""'])
    record('unterminated leading quote and empty read path remain Unknown rather than false Known',
        unclosed.state==empty_path.state=='Unknown',unterminatedState=unclosed.state,emptyPathState=empty_path.state)

    support=root/'cc-cflags';support.write_text(flag+'\n')
    bash=c.flag_descriptor([support],[]);go=c.flag_descriptor([],[flag])
    record('wrapper support tokenizer is separate from source-side Go CGO tokenizer',
        bash.state==go.state=='Known' and str(shell_other) in bash.roots and str(actual_backslash) in go.roots and bash.roots!=go.roots,
        supportAndGoDiffer=bash.roots!=go.roots,goActualRootPresent=str(actual_backslash) in go.roots)

    def discovery():return c.flag_descriptor([],[flag]).footprint
    def restored_write():file.write_text('transient owned bytes\n');file.write_bytes(old)
    outcome='returned'
    try:c.protected(discovery,verify=restored_write)
    except c.Mutated:outcome='Mutated'
    record('protected actual backslash descendant restored mutation refuses instead of omitted-input reuse',outcome=='Mutated',outcome=outcome,endpointBytesRestored=file.read_bytes()==old)

    provisional=discovery();newfile=actual_backslash/'added.h';newfile.write_text('owned added member\n')
    final,obs=c.protected(discovery,provisional=provisional)
    record('Go-selected membership expansion is registered reread and held through final validation',
        str(newfile) in final.members and final.digest!=provisional.digest,
        actualNewMemberPresent=str(newfile) in final.members,observation=obs)

    settings.write_bytes(b'CGO_CPPFLAGS=\xff\n');invalid='returned'
    try:c.read_go_settings(settings)
    except c.Unknown:invalid='Unknown'
    except UnicodeError:invalid='UnicodeError'
    record('unsupported non UTF-8 Go settings fail as Unknown without returning a false Known root',invalid=='Unknown',outcome=invalid)

    path=Path(source) if source else PACKET/'capture.py'
    (case/'source.json').write_text(json.dumps({'sourceReadFrom':str(path.resolve()),'sha256':hashlib.sha256(path.read_bytes()).hexdigest(),
        'ownedRootAnchor':str(PACKET),'actualInstallationTreeScans':0,'goFamilyArgv':[],'qualityOutcomes':[]},indent=2)+'\n')
    return results



class CaptureSemanticTests(unittest.TestCase):
    def test_source_equivalence_controls(self):
        with tempfile.TemporaryDirectory(prefix='completion-capture-semantics-', dir=OUT) as temp:
            results = run(capture, Path(temp) / 'case')
        failures = [item['control'] for item in results if not item['passed']]
        self.assertEqual(len(results), 11)
        self.assertEqual(failures, [])
