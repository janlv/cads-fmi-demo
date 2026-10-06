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

f=Figure('figure-cosimulation-partner-package','Inside the co-simulation platform','Logical responsibilities and interfaces within the “Co-simulation / FMI” box in Figure 3.','LOGICAL ARCHITECTURE',1350)
f.rect(485,375,1055,840,PALE,BLUE,r=22)
f.text(520,415,'Co-simulation / FMI — detail of Figure 3',26,BLUE,weight=700)
f.box(820,215,600,110,'Dashboard / application',['Authorized workflow request and run status'],'gray')
f.edge([(1120,325),(1120,465)],both=True)
f.label(1140,359,'Start / status',anchor='start')
f.rect(35,375,410,440,'#F5F7FA',MUTED,r=20)
f.text(60,415,'Partner integration package',25,INK,weight=700)
f.text(60,447,'Provided by contributing partners',20,MUTED)
f.box(60,475,350,140,['Workflow definitions','(YAML)'],['Models, links and timing'],'gray')
f.box(60,650,350,110,'FMU packages',['Models and resources','Interface documentation'],'gray')
f.text(60,794,'Deliverables, not running services',19,MUTED)
f.box(770,465,730,140,'Run preparation and management',['Validate connections, timing and dependencies','Preserve run identity, versions and authorized scope'])
f.edge([(410,535),(770,535)])
f.label(590,515,'Read configuration')
f.edge([(410,680),(635,680),(635,580),(770,580)])
f.label(650,636,'Load models',anchor='start')
f.edge([(1135,605),(1135,710)],both=True)
f.label(1155,666,'Prepared run / execution status',anchor='start')
f.box(540,710,960,140,'Model coordination and data mapping',['Initialize FMUs; map inputs and model connections','Coordinate communication points; collect outputs and run metadata'])
f.box(60,930,350,140,'Data-exchange interface',['Authorized input data','Results and provenance'],'teal')
f.edge([(410,1000),(465,1000),(465,785),(540,785)],'teal',True)
f.group(530,970,970,195,'PARTICIPATING FMU INSTANCES')
f.edge([(1020,850),(1020,1015)],both=True)
f.label(1040,925,'FMI calls / returned values',anchor='start')
f.s.append(f'<path d="M695 1015 H1335" fill="none" stroke="{BLUE}" stroke-width="2.5"/>')
for x,title in [(555,'FMU A'),(875,'FMU B'),(1195,'FMU …')]:
 f.edge([(x+140,1015),(x+140,1040)],both=True)
 f.box(x,1040,280,95,title,['Model / internal solver'],'teal')
f.text(60,1120,['Partners provide the model packages','and YAML workflow definitions.','The platform interprets and executes','the accepted integration package.'],20,MUTED,leading=28)
f.footer(['The workflow defines model connections; the coordination function manages exchanges between FMUs.',
          'These are logical responsibilities, not prescribed services, libraries, programming languages or scheduling tools.'])
f.save()
