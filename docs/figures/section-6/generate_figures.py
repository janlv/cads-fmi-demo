#!/usr/bin/env python3
"""Generate editable, dependency-free SVG architecture figures."""
from pathlib import Path
from html import escape
from math import hypot

OUT = Path(__file__).resolve().parent
INK = '#172B46'
MUTED = '#53667D'
BLUE = '#2563A6'
TEAL = '#087F8C'
AMBER = '#A96B11'
BORDER = '#B9C9D8'
PALE = '#F3F7FB'

class Figure:
    def __init__(self, name, title, subtitle, status, height=1000):
        self.name, self.height = name, height
        self.s = [f'<svg xmlns="http://www.w3.org/2000/svg" width="1600" height="{height}" viewBox="0 0 1600 {height}" role="img" aria-labelledby="title desc">',
                  f'<title id="title">{escape(title)}</title><desc id="desc">{escape(subtitle)}</desc>',
                  '<defs>']
        for key, color in [('blue', BLUE), ('teal', TEAL), ('amber', AMBER)]:
            self.s.append(f'<marker id="{key}" markerWidth="10" markerHeight="10" refX="8" refY="5" orient="auto-start-reverse" markerUnits="userSpaceOnUse"><path d="M0,0 L10,5 L0,10 Z" fill="{color}"/></marker>')
        self.s += ['</defs>', '<rect width="1600" height="100%" fill="white"/>']
        self.text(60, 48, 'CADS  /  SECTION 6', 17, TEAL, weight=700)
        self.text(1540, 48, status, 16, MUTED, anchor='end', weight=700)
        self.text(60, 102, title, 36, INK, weight=700)
        self.text(60, 141, subtitle, 21, MUTED)
        self.s.append(f'<path d="M60 168 H1540" stroke="{BORDER}"/>')
    def text(self, x, y, lines, size=22, color=INK, anchor='start', weight=400, leading=None):
        if isinstance(lines, str): lines = [lines]
        leading = leading or size * 1.4
        for i, line in enumerate(lines):
            self.s.append(f'<text x="{x}" y="{y+i*leading}" fill="{color}" font-family="Arial, Helvetica, sans-serif" font-size="{size}" font-weight="{weight}" text-anchor="{anchor}">{escape(line)}</text>')
    def rect(self, x,y,w,h,fill='white',stroke=BORDER,dash=False,r=14):
        self.s.append(f'<rect x="{x}" y="{y}" width="{w}" height="{h}" rx="{r}" fill="{fill}" stroke="{stroke}" stroke-width="2"'+(' stroke-dasharray="8 6"' if dash else '')+'/>')
    def box(self,x,y,w,h,title,body=(),kind='blue',dash=False):
        color,fill = {'blue':(BLUE,'#EDF4FD'),'teal':(TEAL,'#EDF9F8'),'amber':(AMBER,'#FFF7E9'),'gray':(MUTED,'#F5F7FA')}[kind]
        self.rect(x,y,w,h,fill,color,dash)
        titles = [title] if isinstance(title,str) else title
        self.text(x+w/2,y+36,titles,23,color,'middle',700,28)
        if body:
            self.text(x+w/2,y+36+28*len(titles)+7,body,19,MUTED,'middle',leading=27)
    def group(self,x,y,w,h,label):
        self.rect(x,y,w,h,PALE,BORDER,r=18)
        self.text(x+22,y+35,label,19,MUTED,weight=700)
    def edge(self,points,kind='blue',both=False,dash=False):
        color={'blue':BLUE,'teal':TEAL,'amber':AMBER}[kind]
        d='M'+' L'.join(f'{x},{y}' for x,y in points)
        self.s.append(f'<path d="{d}" fill="none" stroke="{color}" stroke-width="2.5" stroke-linejoin="round"'+(' stroke-dasharray="7 6"' if dash else '')+'/>')
        # Explicit polygons preserve arrowheads in SVG, AppKit PNG and PDF export.
        def arrow(tip, previous):
            dx, dy = tip[0]-previous[0], tip[1]-previous[1]
            length = hypot(dx,dy)
            ux,uy = dx/length,dy/length
            bx,by = tip[0]-12*ux,tip[1]-12*uy
            vertices = [tip,(bx-5*uy,by+5*ux),(bx+5*uy,by-5*ux)]
            coords = ' '.join(f'{x},{y}' for x,y in vertices)
            self.s.append(f'<polygon points="{coords}" fill="{color}"/>')
        arrow(points[-1],points[-2])
        if both: arrow(points[0],points[1])
    def label(self,x,y,s,kind='blue',anchor='middle'):
        self.text(x,y,s,17,{'blue':BLUE,'teal':TEAL,'amber':AMBER}[kind],anchor)
    def footer(self,lines):
        self.s.append(f'<path d="M60 {self.height-100} H1540" stroke="{BORDER}"/>')
        self.text(60,self.height-68,lines,19,MUTED,leading=28)
    def save(self):
        (OUT/(self.name+'.svg')).write_text('\n'.join(self.s+['</svg>'])+'\n')

f=Figure('01-target-platform','CADS platform: data, computation and access','A target context view for §6.1 — the database and computation remain separate responsibilities.','TARGET ARCHITECTURE')
f.group(315,205,1225,640,'CADS CLOUD PLATFORM  •  OVH deployment context in D3.4')
f.box(345,270,1165,85,'User and application access control',['Apply authorized demonstrator scope to data access, workflow execution and result retrieval.'],'gray')
f.box(60,420,220,180,'Site data',['SCADA / historian','Condition sensors','Market / other data'],'gray')
f.box(345,440,205,140,'Ingestion',['Validate data','Attach metadata'],'teal')
f.box(600,440,220,140,'CADS database',['Inputs + results','Site-scoped data'],'teal')
f.box(870,440,265,140,['Data-access','component'],['Read / persist'],'amber',True)
f.box(1185,440,325,140,['Workflow runtime'],['Execute connected FMUs','Return model outputs'])
for a,b in [(280,345),(550,600)]: f.edge([(a,510),(b,510)],'teal')
f.edge([(820,510),(870,510)],'teal',True)
f.edge([(1135,510),(1185,510)],'teal',True)
f.label(1002,406,'Proposed integration boundary','amber')
f.box(565,680,290,110,'Dashboard / result API',['Authorized result views'])
f.edge([(710,580),(710,680)],'teal')
f.label(729,631,'Stored results','teal',anchor='start')
f.box(1185,680,325,110,'Partner model packages',['Accepted Co-Simulation FMUs'],'gray')
f.edge([(1347,680),(1347,580)])
f.label(1365,631,'Load models',anchor='start')
f.text(350,820,'Logical site segregation is shown; physical database topology is a separate decision.',18,MUTED)
f.footer(['Target view, not a deployment-completion claim. Amber dashed box: connector design to agree with Kaizen.',
          'Prototype coverage: workflow execution and demo dashboard; database read/write and full access control remain pending.'])
f.save()

f=Figure('02-modules-and-workflows','One execution contract, different model roles','A module view for §6.2 — workflows choose the connections; module categories do not impose an execution order.','TARGET MODULE VIEW')
f.box(60,215,1480,115,'Declarative workflow (YAML)',['Select FMUs • map inputs and outputs • declare data requirements • configure supported timing'])
xs=[60,440,820,1200]
modules=[('Physical modeling',['Reduced-order models','or lookup tables']),(['Condition monitoring','and maintenance'],['Indicators, degradation','and remaining useful life']),('Optimization',['Operational or economic','decision variables']),('Decision support',['Combine results into','assessments and choices'])]
# A common selection rail, not a numerical coupling graph.
f.edge([(800,330),(800,365)])
f.s.append(f'<path d="M230 365 H1370" stroke="{BLUE}" stroke-width="2.5" fill="none"/>')
for x,(title,body) in zip(xs,modules):
    f.edge([(x+170,365),(x+170,415)])
    f.box(x,415,340,175,title,body,'teal')
    f.edge([(x+170,590),(x+170,710)],'teal',True)
f.label(800,650,'Standard FMI inputs, outputs and lifecycle calls','teal')
f.box(60,710,1480,115,'CADS workflow runtime',['Interpret the workflow • manage FMU lifecycles • exchange values • collect outputs'])
f.footer(['The same runtime interface applies to all module roles. Numerical connections are defined by each workflow.',
          'Prototype: sequential final-output handoff with placeholder models. Further coupling patterns require implementation and validation.'])
f.save()

f=Figure('03-prototype-execution-stack','Inside the prototype: launch, execute, return results','A technical view for §6.3 — hosted dashboard path; each submitted workflow runs its FMUs in one runner process.','CURRENT PROTOTYPE',1200)
f.group(60,205,340,835,'LOCAL USER MACHINE')
f.group(430,205,430,835,'KAIZEN / KUBERNETES CONTROL')
f.group(900,205,640,835,'WORKFLOW POD  /  BUNDLED IMAGE')
f.box(85,285,290,105,'Browser dashboard',['Select a workflow; view results'])
f.box(85,470,290,150,'Go HTTP service',['Serve UI and API','Build Argo manifest','Parse result JSON'])
f.box(85,765,290,110,'Argo CLI',['Submit • status • logs'])
f.edge([(230,390),(230,470)],both=True)
f.label(250,436,'HTTP',anchor='start')
f.edge([(230,620),(230,765)],both=True)
f.label(250,700,'Subprocess calls',anchor='start')
f.box(475,765,340,110,'Argo API server',['Workflow and log access'])
f.box(475,535,340,110,'Kubernetes API',['Workflow and pod resources'])
f.box(475,285,340,125,'Argo workflow controller',['Reconcile workflow resources','Request workflow pods'])
f.edge([(375,820),(475,820)],both=True)
f.label(425,801,'HTTPS')
f.edge([(645,765),(645,645)],both=True)
f.label(665,710,'Create / query',anchor='start')
f.edge([(645,535),(645,410)],both=True)
f.label(665,477,'Watch / update',anchor='start')
f.edge([(815,347),(955,347)])
f.label(885,326,'Launch¹')
f.box(955,285,530,125,'Go runner + workflow.Executor',['Read YAML; resolve CSV / S3 / synthetic inputs','Execute steps; collect final values and traces'])
f.box(955,470,530,90,'Go cgo boundary + C++ bridge',['Translate configuration and control the FMI lifecycle'])
f.box(955,620,530,90,'FMIL  /  FMI Library',['Load FMU package, metadata and native library'])
f.box(955,770,530,115,'Active FMU instance',['Initialize → set inputs → doStep → get outputs','Terminate and free before the next workflow step'],'teal')
for y1,y2 in [(410,470),(560,620),(710,770)]: f.edge([(1220,y1),(1220,y2)],both=True)
f.box(955,945,530,65,'Runner stdout → pod logs',kind='teal')
f.edge([(1485,348),(1515,348),(1515,917),(1220,917),(1220,945)],'teal')
f.edge([(955,978),(645,978),(645,875)],'teal')
f.label(785,958,'JSON results via Argo logs','teal')
f.text(85,935,['No browser-to-Argo connection.','The local service uses configured','infrastructure credentials.'],17,MUTED,leading=26)
f.text(475,1010,'¹ Through Kubernetes scheduling and pod startup.',16,MUTED)
f.footer(['Blue: calls and execution control (two-headed arrows include responses). Teal: FMU component and hosted result path.',
          'Image bundles runner, FMIL, FMUs, YAML and dependencies. CSV/S3 inputs are supported; there is no integrated database read/write.'])
f.save()

f=Figure('04-proposed-data-access','Shared data access: a workflow run from start to finish','A proposed integration view — option 2 separates database responsibilities from the model FMUs.','PROPOSAL  /  TO AGREE WITH KAIZEN',1100)
# Sequence diagram: lifelines and messages are deliberately not deployment instances.
centers=[230,620,1010,1390]
heads=[('CADS database',['Authorized site data'],'teal'),('Data-access component',['Common adapter implementation'],'amber'),('Workflow runner',['Run lifecycle and data routing'],'blue'),('Model FMUs',['Standard FMI interfaces'],'teal')]
for c,(title,body,k) in zip(centers,heads):
    f.box(c-165,220,330,105,title,body,k,k=='amber')
    f.s.append(f'<path d="M{c} 325 V900" stroke="{BORDER}" stroke-width="2" stroke-dasharray="5 6"/>')
def message(y,a,b,label,kind='teal'):
    f.edge([(centers[a],y),(centers[b],y)],kind)
    f.label((centers[a]+centers[b])/2,y-14,label,kind)
message(385,2,1,'1  Request scoped input dataset','blue')
message(455,1,0,'2  Read approved time window')
message(525,0,1,'Data + source metadata')
message(595,1,2,'3  Deliver snapshot / buffered inputs')
message(665,2,3,'4  Initialize and step','blue')
message(735,3,2,'Outputs / traces')
message(805,2,1,'5  Submit results + provenance')
message(875,1,0,'6  Persist outputs + metadata')
f.rect(60,930,1480,60,'#FFF7E9',AMBER,True)
f.text(800,968,'Deployment choice remains open: workflow adapter, service, or FMU with explicit runtime lifecycle support.',21,AMBER,'middle')
f.footer(['Proposed batch-oriented contract: preserve site scope, input-batch ID, run ID, model/workflow versions and timestamps.',
          'The current runner cannot keep a connector FMU alive between steps. A background FMU needs additional coordination.'])
f.save()
print('Generated four SVG figures in', OUT)
