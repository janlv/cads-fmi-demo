#!/usr/bin/env python3
"""Generate editable, dependency-free SVG architecture figures."""
from pathlib import Path
from html import escape
from math import hypot

OUT = Path(__file__).resolve().parents[2] / 'deliverables' / 'section-6' / 'assets'
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
        self.text(x+w/2,y+36,titles,28,color,'middle',700,28)
        if body:
            self.text(x+w/2,y+36+28*len(titles)+7,body,24,MUTED,'middle',leading=30)
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
        self.text(x,y,s,21,{'blue':BLUE,'teal':TEAL,'amber':AMBER}[kind],anchor)
    def footer(self,lines):
        self.s.append(f'<path d="M60 {self.height-100} H1540" stroke="{BORDER}"/>')
        self.text(60,self.height-68,lines,19,MUTED,leading=28)
    def save(self):
        (OUT/(self.name+'.svg')).write_text('\n'.join(self.s+['</svg>'])+'\n')

f=Figure('figure-3-application-access','Application access: identity, permissions and resources','Authentication establishes identity; authorization controls access to workflows, model packages and demonstrator data.','CADS APPLICATION ARCHITECTURE',1100)
f.box(60,215,420,110,'Users',['Access through the dashboard'],'gray')
f.box(590,215,420,110,'Authentication',['User identity + application identity'])
f.box(1120,215,420,110,'Applications / workloads',['Use their own service identities'],'gray')
f.edge([(480,270),(590,270)])
f.edge([(1120,270),(1010,270)])
f.edge([(800,325),(800,420)])
f.label(820,379,'Verified identity',anchor='start')
f.box(590,420,420,110,'Authorization',['Roles • demonstrator scope','Workflow and model permissions'])
f.edge([(800,530),(800,620)])
f.s.append(f'<path d="M270 575 H1330" fill="none" stroke="{BLUE}" stroke-width="2.5"/>')
f.edge([(270,575),(270,620)])
f.edge([(1330,575),(1330,620)])
f.label(270,558,'Authorized application access')
f.label(1330,558,'Authorized workflow execution')
f.box(60,620,420,110,'Application service / API',['Dashboard requests; retrieve results'])
f.box(590,620,420,110,'Data-access component',['Scoped reads and writes'],'teal')
f.box(1120,620,420,110,'Workflow runtime / FMI',['Execute authorized workflows'])
f.edge([(480,675),(590,675)],'teal',True)
f.edge([(1010,675),(1120,675)],'teal',True)
f.label(535,651,'Results','teal')
f.label(1065,651,'I/O','teal')
f.box(590,825,420,150,'STOR-HY database',['Separate demonstrator data scopes','Vouglans • Le Cheylas • La Rance','Alqueva • Vilarinho • Pozu Figaredo'],'teal')
f.edge([(800,730),(800,825)],'teal',True)
f.label(820,786,'Inputs + stored results','teal',anchor='start')
f.box(1120,825,420,150,'Partner model libraries',['Versioned FMU packages','NORCE • Andritz • HESSO','UPC • INPG • other contributors'],'gray')
f.edge([(1330,825),(1330,730)])
f.label(1350,786,'Load approved FMUs',anchor='start')
f.text(60,848,['Authorization applies at each','protected service boundary.','Model access and data access','are separate permissions.'],21,MUTED,leading=32)
f.footer(['Blue: identity, permissions and execution access. Teal: data requests and responses.',
          'Database scopes are logical access boundaries; this diagram does not prescribe separate physical database instances.'])
f.save()

f=Figure('figure-2a-execution-roles','Infrastructure scheduling and model coordination','Infrastructure starts and supervises the execution environment; the runtime coordinates the models inside it.','CADS EXECUTION ARCHITECTURE',1030)
f.group(60,210,470,675,'INFRASTRUCTURE ORCHESTRATION')
f.group(690,210,850,675,'WORKFLOW EXECUTION ENVIRONMENT / POD')
f.box(90,285,410,110,'CADS application service',['Authorize request','Submit workflow job'])
f.box(90,485,410,110,'Argo Workflows',['Manage dependencies and lifecycle','Track execution status'])
f.box(90,740,410,100,'Kubernetes',['Allocate resources; isolate runs'])
f.edge([(295,395),(295,485)])
f.label(315,446,'Submit job',anchor='start')
f.edge([(295,595),(295,740)])
f.label(315,671,'Create / monitor workload',anchor='start')
f.edge([(500,790),(690,790)])
f.label(595,768,'Start pod')
f.box(740,285,750,110,'Go workflow runtime',['Read YAML and run context; manage FMU lifecycles','Coordinate coupling, communication timing and result collection'])
f.box(740,455,750,85,'cgo / C++ bridge',['Translate Go configuration into FMI calls'])
f.box(740,600,750,85,'FMI Library (FMIL)',['Load model packages and invoke their FMI functions'])
f.edge([(1115,395),(1115,455)],both=True)
f.edge([(1115,540),(1115,600)],both=True)
f.edge([(1115,685),(1115,720)],both=True)
f.s.append(f'<path d="M855 720 H1375" fill="none" stroke="{BLUE}" stroke-width="2.5"/>')
for x,title in [(740,'FMU A'),(1000,'FMU B'),(1260,'FMU …')]:
 f.edge([(x+115,720),(x+115,760)],both=True)
 f.box(x,760,230,85,title,['Model / solver'],'teal')
f.footer(['The workflow defines the FMU connections. The Go runtime manages model time and exchanges values through FMI calls.',
          'Argo and Kubernetes manage jobs and resources. Two-headed arrows represent calls and returned values.'])
f.save()
print('Generated application-access and execution-role figures.')
