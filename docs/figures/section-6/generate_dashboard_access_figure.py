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

f=Figure('figure-3-dashboard-access','Application access through the dashboard','Users start workflows and view data through the dashboard; data exchange remains internal to CADS.','LOGICAL ARCHITECTURE',1390)
f.rect(35,375,1530,870,PALE,BLUE,r=22)
f.text(70,414,'CADS cloud infrastructure',26,BLUE,weight=700)
f.text(70,449,'OVH Cloud — detail of Figure 2',22,MUTED)
f.box(590,215,420,110,'Users',['Project partners'],'gray')
f.edge([(800,325),(800,465)],both=True)
f.box(590,465,420,140,'Dashboard / application',['User entry point','Start workflows; view data'])
f.box(60,465,420,140,['Authentication and','authorization'],['Identity • roles • site scope','Workflow and model permissions'])
f.edge([(590,535),(480,535)],both=True)
f.label(535,510,'Checks')
f.edge([(1010,535),(1330,535),(1330,780)],both=True)
f.label(1280,510,'Start workflow / status')
f.box(590,780,420,110,'Data-exchange interface',['Internal, scoped reads and writes'],'teal')
f.box(1120,780,420,110,'Co-simulation / FMI',['Execute authorized workflows'])
f.edge([(800,605),(800,780)],'teal',True)
f.label(820,706,'View data / results','teal',anchor='start')
f.edge([(1010,835),(1120,835)],'teal',True)
f.label(1065,811,'I/O','teal')
f.box(590,1020,420,150,'STOR-HY database',['Separate demonstrator data scopes','Vouglans • Le Cheylas • La Rance','Alqueva • Vilarinho • Pozu Figaredo'],'teal')
f.edge([(800,890),(800,1020)],'teal',True)
f.label(820,963,'Inputs + stored results','teal',anchor='start')
f.box(1120,1020,420,150,'Partner model libraries',['Versioned FMU packages','NORCE • Andritz • HESSO','UPC • INPG • other contributors'],'gray')
f.edge([(1330,1020),(1330,890)])
f.label(1350,963,'Use approved FMUs',anchor='start')
f.text(70,1024,['Internal requests and runs','retain the initiating identity','and authorized site scope.','Permissions are enforced','at protected interfaces.'],23,MUTED,leading=32)
f.footer(['Blue: user interaction, access checks and workflow control. Teal: internal data requests and responses.',
          'The enclosing boundary expands Figure 2; internal boxes are logical interfaces, not prescribed software or deployment units.'])
f.save()
