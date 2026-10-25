#!/usr/bin/env python3
"""Typed migration/census for Analyzer.exprTypes. No Go execution required.

Production source-policy tests additionally use Go's AST to close non-index uses.
This transformer preserves writes and rewrites reads/comma-ok/deletes only.
"""
import argparse,json,re
from pathlib import Path
TOKEN=re.compile(r'//[^\n]*|/\*.*?\*/|`[^`]*`|"(?:\\.|[^"\\])*"|\'(?:\\.|[^\'\\])*\'|[A-Za-z_][A-Za-z_0-9]*|:=|==|!=|<=|>=|\+=|-=|\*=|/=|\+\+|--|&&|\|\||<-|[^\s]',re.S)
SKIP={'analyzer_expr_type_overlay.go'}
def tokens(source):
 return [(m.group(),m.start(),m.end()) for m in TOKEN.finditer(source) if not m.group().startswith(('//','/*'))]
def migrate(source):
 ts=tokens(source);edits=[];counts={'read':0,'comma_ok':0,'write':0,'delete':0}
 for i,t in enumerate(ts):
  if t[0]!='exprTypes' or i<2 or ts[i-1][0]!='.':continue
  start=i-2
  while start>=2 and ts[start-1][0]=='.' and re.fullmatch(r'[A-Za-z_]\w*',ts[start-2][0]):start-=2
  receiver=source[ts[start][1]:ts[i-1][1]]
  if i+1<len(ts) and ts[i+1][0]=='[':
   level=1;j=i+2
   while j<len(ts) and level:
    if ts[j][0]=='[':level+=1
    if ts[j][0]==']':level-=1
    j+=1
   if level:raise ValueError('unterminated exprTypes index')
   close=j-1;after=ts[j][0] if j<len(ts) else ''
   if after in ('=','+=','-=','*=','/=','++','--'):
    counts['write']+=1;continue
   comma=False
   if start>3 and ts[start-1][0] in (':=','=') and ts[start-3][0]==',' and re.fullmatch(r'[A-Za-z_]\w*',ts[start-2][0]) and re.fullmatch(r'[A-Za-z_]\w*',ts[start-4][0]):
    gap=source[ts[close][2]:ts[j][1]] if j<len(ts) else '\n'
    comma=after in (';','}') or '\n' in gap or not after
   kind='comma_ok' if comma else 'read';counts[kind]+=1
   method='lookupExprType' if comma else 'exprType'
   edits.append((ts[start][1],ts[i+1][2],receiver+'.'+method+'('));edits.append((ts[close][1],ts[close][2],')'))
  elif i+1<len(ts) and ts[i+1][0]==',' and start>=2 and ts[start-1][0]=='(' and ts[start-2][0]=='delete':
   counts['delete']+=1;edits.append((ts[start-2][1],ts[i+1][2],receiver+'.deleteExprType('))
 for begin,end,value in sorted(edits,reverse=True):source=source[:begin]+value+source[end:]
 return source,counts

def main():
 parser=argparse.ArgumentParser();parser.add_argument('--root',type=Path,default=Path(__file__).resolve().parents[1]/'src'/'semantic');parser.add_argument('--check',action='store_true');parser.add_argument('--manifest',type=Path);args=parser.parse_args();manifest={};bad=[]
 for path in sorted(args.root.glob('*.go')):
  if path.name.endswith('_test.go') or path.name in SKIP:continue
  source=path.read_text();updated,counts=migrate(source)
  if any(counts.values()):manifest[path.name]=counts
  if source!=updated:
   if args.check:bad.append(path.name)
   else:path.write_text(updated)
 if args.manifest:args.manifest.write_text(json.dumps(manifest,indent=2)+'\n')
 if bad:raise SystemExit('unmigrated Analyzer.exprTypes operations: '+', '.join(bad))
if __name__=='__main__':main()
