#!/bin/python3
"""Bounded source-scoped input identities, not production completion/quality results.
No Go/Node/compiler argv. Known component scopes never imply a Known whole profile.
Only named tool/package roots are read; tree bytes are hashed, never snapshotted.
"""
from dataclasses import dataclass
from pathlib import Path
import ctypes, hashlib, json, os, platform, re, shutil, stat, struct

REPO_ROOT = Path(__file__).resolve().parents[2]

class Unknown(Exception): pass
class Mutated(Exception): pass
class Unavailable(Exception): pass

@dataclass(frozen=True)
class Limits:
    entries: int = 65536
    bytes: int = 768 * 1024 * 1024

@dataclass
class Footprint:
    # Internal membership is needed for watches, not a persisted approval manifest.
    members: dict[str, str]
    digest: str
    files: int
    directories: int
    bytes: int

    def report(self):
        return {'digest':self.digest, 'files':self.files, 'directories':self.directories,
                'bytesHashed':self.bytes, 'membershipCount':len(self.members)}

@dataclass
class Component:
    name: str
    scope: str
    state: str
    footprint: Footprint
    limitation: str = ''
    def report(self):
        return {'name':self.name,'scope':self.scope,'state':self.state,
                'identity':self.footprint.report(),'limitation':self.limitation}


def identity(paths, trees=(), limits=Limits()):
    members = {}; sizes = {}; dirs = set(); total_bytes = 0; work = [Path(p) for p in paths]
    roots = [Path(p).absolute().resolve() for p in trees]
    tree_jobs = [Path(p).absolute() for p in trees]
    # All selector chains matter, including a changed symlink to identical target bytes.
    def selectors(path):
        for candidate in (path, *path.parents):
            if candidate.is_symlink():
                s=candidate.lstat()
                members[str(candidate)]='link:'+str((s.st_dev,s.st_ino,s.st_mode))+':'+os.readlink(candidate)
    def one(path):
        nonlocal total_bytes
        path=Path(os.path.abspath(path)); selectors(path)
        if len(members) >= limits.entries: raise Unknown('input membership exceeds bounded entry budget')
        try: s=path.lstat()
        except FileNotFoundError:
            members[str(path)]='absent'; return
        if stat.S_ISLNK(s.st_mode):
            target=path.resolve(strict=True)
            members[str(path)]='link:'+str((s.st_dev,s.st_ino,s.st_mode))+':'+os.readlink(path)
            if roots and not any(target.is_relative_to(r) for r in roots):
                raise Unknown('tree symlink leaves its source-accounted root: '+str(path))
            if target.is_file(): one(target)
            elif not target.is_dir(): raise Unknown('unsupported link target type: '+str(path))
            # Directory aliases need graph/cycle handling beyond this supported profile.
            else: raise Unknown('directory symlink requires additional accounted root: '+str(path))
        elif stat.S_ISREG(s.st_mode):
            if str(path) in members: return
            if total_bytes + s.st_size > limits.bytes: raise Unknown('input bytes exceed bounded read budget')
            h=hashlib.sha256(); file_bytes=0
            with path.open('rb') as f:
                while True:
                    # A stale size must not allow an unbounded provisional read.
                    chunk=f.read(min(1024*1024,limits.bytes-total_bytes+1))
                    if not chunk: break
                    total_bytes += len(chunk); file_bytes += len(chunk)
                    if total_bytes > limits.bytes: raise Unknown('actual input bytes exceed bounded read budget')
                    h.update(chunk)
            # Not a final stability test: the observer/reread provides that interval.
            members[str(path)]='file:'+str((s.st_dev,s.st_ino,s.st_mode,file_bytes))+':'+h.hexdigest()
            sizes[str(path)]=file_bytes
        elif stat.S_ISDIR(s.st_mode):
            members[str(path)]='dir:'+str((s.st_dev,s.st_ino,s.st_mode)); dirs.add(str(path))
        else: raise Unknown('unsupported source input type: '+str(path))
    def enumeration_error(error):
        raise error  # os.walk otherwise silently skips unreadable descendant membership.
    for root in tree_jobs:
        one(root)
        if not root.exists(): continue
        for parent, children, files in os.walk(root,followlinks=False,onerror=enumeration_error):
            one(Path(parent))
            for name in sorted(children+files): one(Path(parent)/name)
    for path in work: one(path)
    digest=hashlib.sha256(json.dumps(sorted(members.items()),separators=(',',':')).encode()).hexdigest()
    return Footprint(members,digest,len(sizes),len(dirs),total_bytes)


def tree_component(name, root, scope, paths=()):
    root=Path(root)
    try:
        fp=identity(paths,trees=(root,))
        return Component(name,scope,'Known' if root.is_dir() else 'Unknown',fp,
            '' if root.is_dir() else 'Root absent: ordinary full may provision/capture it under existing policy; no manual preinstall required')
    except (Unknown,OSError,ValueError) as exc:
        return Component(name,scope,'Unknown',identity((root,)),str(exc))


# A recognized literal support-read graph, not a general shell parser/interpreter.
SOURCE_LINE=re.compile(r'^\s*source\s+(/[^\s;"\']+)\s*$',re.M)
READ_FILE=re.compile(r'\$\(<\s*(/[^\s)]+)\s*\)')
SUPPORT_LITERAL=re.compile(r'/nix/store/[A-Za-z0-9._+-]+/nix-support/[A-Za-z0-9._+-]+')
NIX_PATH=re.compile(r'/nix/store/[a-z0-9]{32}-[A-Za-z0-9._+-]+(?:/[A-Za-z0-9._+/-]+)?')


def wrapper_reads(wrapper):
    """Close the named wrapper's literal sourced/support-file read seam only.
    Include conditional absent hooks/read files. Flag-derived compiler/sysroot roots
    are reported separately and never certified by these small file identities.
    """
    wrapper=Path(wrapper).absolute().resolve(); queue=[wrapper]; scripts=[]; inputs={wrapper}; edges=[]; external=set()
    while queue:
        current=queue.pop(0)
        if current in scripts: continue
        body=current.read_text(); scripts.append(current)
        if re.search(r'^\s*\.\s+',body,re.M):
            raise Unknown('unaccounted dot-source syntax in '+str(current))
        if (current.name=='cc-wrapper-hook' or current.name.startswith('add-local-')) and any(
                line.strip() and not line.lstrip().startswith('#') for line in body.splitlines()):
            raise Unknown('custom executable wrapper hook requires source accounting: '+str(current))
        source_lines=[l.strip() for l in body.splitlines() if l.lstrip().startswith('source ')]
        targets=[Path(p) for p in SOURCE_LINE.findall(body)]
        if len(source_lines)!=len(targets): raise Unknown('unaccounted dynamic source in '+str(current))
        for target in targets:
            inputs.add(target); edges.append((str(current), 'source', str(target)))
            if target.is_file(): queue.append(target)
            elif 'cc-wrapper-hook' not in target.name and 'add-local-' not in target.name:
                raise Unknown('mandatory wrapper source missing: '+str(target))
        for text in SUPPORT_LITERAL.findall(body): inputs.add(Path(text))
        for text in READ_FILE.findall(body):
            inputs.add(Path(text)); edges.append((str(current),'read',text))
        for literal in NIX_PATH.findall(body):
            if '/nix-support/' not in literal: external.add(literal)
    # Data flag files name further include/library/loader roots. Identifying the
    # flag file bytes alone does NOT close those transitive readers.
    for file in sorted(inputs):
        if file.is_file() and file not in scripts:
            for literal in NIX_PATH.findall(file.read_text()): external.add(literal)
    fp=identity(sorted(inputs))
    return Component('nix-gcc-wrapper-read-files','literal sourced scripts + guarded support flag/hook reads','Known',fp), {
        'scripts': [str(p) for p in scripts], 'edges':edges,
        'guardedAbsentInputs':[str(p) for p in sorted(inputs) if not p.exists()],
        'furtherToolAndIncludeRoots':sorted(external),
        'completeCompilerClosure':False}


def ordinary_components(env):
    """Automatic actual known-root membership/content. No effective metadata is guessed."""
    components=[]; info={}; tools={name:shutil.which(name,path=env.get('PATH','')) for name in ('go','gofmt','staticcheck','node','cc','gcc')}
    info['toolBindings']={name:{'lexical':p,'resolved':str(Path(p).resolve())} if p else None for name,p in tools.items()}
    binding_candidates=[]
    for name,selected in tools.items():
        for directory in env.get('PATH','').split(os.pathsep):
            candidate=Path(directory or '.')/name;binding_candidates.append(candidate)
            if selected and os.path.abspath(candidate)==os.path.abspath(selected):break
    components.append(Component('path-tool-bindings','priority PATH candidate content/absence + selected symlink-chain identity',
        'Known',identity(binding_candidates)))
    if tools['go']:
        root=Path(env.get('GOROOT') or Path(tools['go']).resolve().parent.parent)
        go_component=tree_component('go-installation',root,'complete installation byte/mode/link/membership identity',
            (tools['go'],tools['gofmt']) if tools['gofmt'] else (tools['go'],))
        if not all((root/p).exists() for p in ('VERSION','src/runtime','pkg/tool','bin/go','bin/gofmt')):
            go_component.state='Unknown';go_component.limitation='Opaque/nonstandard Go binding: full capture must identify actual selected installation'
        components.append(go_component)
    for name in ('staticcheck','node'):
        if tools[name]:
            components.append(Component(name+'-executable','resolved executable bytes/binding; external runtime separately required',
                'Known',identity((tools[name],))))
    home=Path(env['HOME']); config=Path(env.get('XDG_CONFIG_HOME') or home/'.config')
    goenv=Path(env.get('GOENV') or config/'go/env')
    paths=[] if env.get('GOENV')=='off' else [goenv]
    components.append(Component('go-user-settings','selected settings file byte/binding identity; full captures effective settings',
        'Known',identity(paths)))
    # Source exact pinned host-module name; full capture remains authority for effective cache path.
    source=(REPO_ROOT/'tools'/'pack-binaries'/'toolchain.go').read_text()
    version=re.search(r'const Toolchain = "(go[0-9.]+)"',source).group(1)
    candidate=Path(env.get('GOMODCACHE') or home/'go/pkg/mod')/('golang.org/toolchain@v0.0.1-'+version+'.linux-amd64')
    components.append(tree_component('official-pinned-distribution-candidate',candidate,
        'candidate distribution bytes, not captured effective GOMODCACHE/proxy/checksum readiness'))
    info['officialCandidateIsNotCapturedMetadata']=True
    prefixes=[Path(env[k]) for k in ('NPM_CONFIG_PREFIX','npm_config_prefix') if env.get(k)]
    if tools['node']: prefixes.append(Path(tools['node']).resolve().parent.parent)
    prefixes.append(home/'.npm-global')
    candidates=[]
    if env.get('YOLO_TEST_PI_PACKAGE'): candidates.append(Path(env['YOLO_TEST_PI_PACKAGE']))
    candidates.extend(p/'lib/node_modules/@earendil-works/pi-coding-agent' for p in prefixes)
    candidates=list(dict.fromkeys(candidates))
    selection=identity([p/'dist/index.js' for p in candidates])
    components.append(Component('pi-package-selection','priority candidate sentinel CONTENT/binding identity incl absence', 'Known',selection))
    selected=next((p for p in candidates if (p/'dist/index.js').is_file()),None)
    if selected:
        components.append(tree_component('pi-installation',selected,
            'complete installed package + nested node_modules bytes; ancestor/dynamic resolution separately required'))
    info['selectedPiRoot']=str(selected) if selected else None
    sub=Path(env.get('YOLO_TEST_PI_SUBAGENTS') or home/'.pi/agent/git/github.com/mschulkind/pi-subagents')
    # The test imports both required registry and launch planner, not just its skip sentinel.
    components.append(tree_component('pi-subagents-source',sub/'src',
        'whole src byte/mode/membership superset of two harness imports', (sub/'package.json',)))
    for name in ('cc','gcc'):
        if tools[name]:
            try:
                wrapper, graph=wrapper_reads(tools[name]); components.append(wrapper); info['wrapperReadGraph']=graph
                break
            except (Unknown,OSError,UnicodeError) as exc:
                info['wrapperUnknown']=str(exc)
    info['profileState']='Unknown'
    info['exactNextSeam']='Capture effective full Go metadata/checksum-selected distribution and close actual flag-derived compiler/sysroot/loader reads; component identities alone are not full outcomes'
    return components,info


class Monitor:
    MASK=0x2|0x4|0x8|0x40|0x80|0x100|0x200|0x400|0x800
    def __init__(self):
        if platform.system()!='Linux': raise Unavailable('Linux supported fixture only')
        self.lib=ctypes.CDLL(None,use_errno=True)
        self.lib.inotify_init1.argtypes=[ctypes.c_int];self.lib.inotify_init1.restype=ctypes.c_int
        self.lib.inotify_add_watch.argtypes=[ctypes.c_int,ctypes.c_char_p,ctypes.c_uint32];self.lib.inotify_add_watch.restype=ctypes.c_int
        self.fd=self.lib.inotify_init1(os.O_CLOEXEC|os.O_NONBLOCK)
        if self.fd<0: raise Unavailable('inotify init unavailable')
        self.paths=set();self.by_wd={};self.children={};self.full_directories=set()
        self.events=[];self.ignored_events=[];self.lost=False
    def register(self,footprint):
        for path,value in footprint.members.items():
            p=Path(path)
            if value.startswith('dir:'): self.full_directories.add(str(p))
            child=p
            for parent in p.parents:
                self.children.setdefault(str(parent),set()).add(child.name);child=parent
            for candidate in (p,*p.parents):
                if str(candidate) in self.paths or not candidate.exists():continue
                if len(self.paths)>=65536:raise Unavailable('bounded watch budget exceeded')
                wd=self.lib.inotify_add_watch(self.fd,os.fsencode(candidate),self.MASK)
                if wd<0:
                    self.drain();raise Unavailable('watch registration unavailable')
                self.paths.add(str(candidate));self.by_wd.setdefault(wd,set()).add(str(candidate))
    def drain(self):
        while True:
            try:data=os.read(self.fd,262144)
            except BlockingIOError:break
            except OSError as exc:
                if self.events:raise Mutated('recorded mutation precedes observer error') from exc
                raise Unavailable('observer read error') from exc
            if not data:raise Unavailable('observer lost')
            offset=0
            while offset<len(data):
                wd,mask,cookie,size=struct.unpack_from('iIII',data,offset)
                name=os.fsdecode(data[offset+16:offset+16+size].split(b'\0',1)[0]);offset+=16+size
                if mask&0x4000:self.lost=True
                else:
                    paths=self.by_wd.get(wd,set())
                    event={'paths':sorted(paths),'mask':mask,'child':name}
                    relevant=(not name or not paths or any(p in self.full_directories or
                        name in self.children.get(p,set()) for p in paths))
                    (self.events if relevant else self.ignored_events).append(event)
        if self.events:raise Mutated('mutation/replacement/alias event: '+json.dumps(self.events[:8]))
        if self.lost:raise Unavailable('event overflow: full/no reusable publication')
    def close(self):os.close(self.fd)


def protected(discover,verify=lambda:None,provisional=None,before_registration=None):
    """All identities are provisional until registration + full reread stabilize.
    Protects verify through final reread/drain; no production publication is claimed.
    """
    current=provisional or discover()
    if before_registration:before_registration()
    monitor=Monitor()
    try:
        for attempt in range(4):
            monitor.register(current); monitor.drain()
            try:reread=discover()
            except Exception:
                monitor.drain();raise  # Decoded mutation wins over discovery/availability failure.
            monitor.drain()
            if reread.digest==current.digest and reread.members==current.members:break
            current=reread
        else:raise Unavailable('membership did not stabilize')
        verify()
        monitor.drain()
        try:final=discover()
        except Exception:
            monitor.drain();raise
        monitor.register(final);monitor.drain()
        if final.digest!=reread.digest or final.members!=reread.members:raise Mutated('final input context differs')
        return final,{'watchCount':len(monitor.paths),'protectedThroughFinalRereadAndDrain':True,
            'irrelevantAncestorSiblingEvents':len(monitor.ignored_events),
            'endpointEqualityIsNotMutationProof':True,'prewatchEventsNotClaimed':True}
    finally:monitor.close()
