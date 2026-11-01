"""Test fixture: a DOCX shaped like one Word saves (styles, settings, font table, web settings, theme,
numbering, and the document properties with the author and the company in them).

Run as a script: docx_wordlike.py OUT [EXTRA_STYLES_XML [SETTINGS_EXTRA_XML]]."""
import sys, zipfile
W = 'http://schemas.openxmlformats.org/wordprocessingml/2006/main'
R = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'
CT = 'application/vnd.openxmlformats-officedocument.'
extra = sys.argv[2] if len(sys.argv) > 2 else ''
settings_extra = sys.argv[3] if len(sys.argv) > 3 else ''
parts = {
 'word/styles.xml': ('wordprocessingml.styles+xml', 'styles', f'<w:styles xmlns:w="{W}"><w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:eastAsia="等线"/><w:sz w:val="21"/></w:rPr></w:rPrDefault></w:docDefaults><w:style w:type="paragraph" w:default="1" w:styleId="a"><w:name w:val="Normal"/><w:qFormat/></w:style><w:style w:type="paragraph" w:styleId="1"><w:name w:val="heading 1"/><w:basedOn w:val="a"/><w:next w:val="a"/><w:uiPriority w:val="9"/><w:pPr><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:sz w:val="44"/></w:rPr></w:style>{extra}</w:styles>'),
 'word/settings.xml': ('wordprocessingml.settings+xml', 'settings', f'<w:settings xmlns:w="{W}"><w:zoom w:percent="100"/><w:defaultTabStop w:val="420"/><w:characterSpacingControl w:val="compressPunctuation"/><w:compat><w:compatSetting w:name="compatibilityMode" w:uri="http://schemas.microsoft.com/office/word" w:val="15"/></w:compat><w:rsids><w:rsidRoot w:val="00A1B2C3"/><w:rsid w:val="00A1B2C3"/></w:rsids>{settings_extra}</w:settings>'),
 'word/fontTable.xml': ('wordprocessingml.fontTable+xml', 'fontTable', f'<w:fonts xmlns:w="{W}"><w:font w:name="等线"><w:altName w:val="DengXian"/><w:panose1 w:val="02010600030101010101"/><w:charset w:val="86"/><w:family w:val="auto"/><w:pitch w:val="variable"/></w:font></w:fonts>'),
 'word/webSettings.xml': ('wordprocessingml.webSettings+xml', 'webSettings', f'<w:webSettings xmlns:w="{W}"><w:optimizeForBrowser/></w:webSettings>'),
 'word/numbering.xml': ('wordprocessingml.numbering+xml', 'numbering', f'<w:numbering xmlns:w="{W}"><w:abstractNum w:abstractNumId="0"><w:nsid w:val="1A2B3C4D"/><w:multiLevelType w:val="hybridMultilevel"/><w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="chineseCounting"/><w:lvlText w:val="第%1章"/><w:lvlJc w:val="left"/></w:lvl><w:lvl w:ilvl="1"><w:numFmt w:val="decimal"/><w:lvlText w:val="（%2）"/></w:lvl><w:lvl w:ilvl="2"><w:numFmt w:val="bullet"/><w:lvlText w:val=""/><w:rPr><w:rFonts w:ascii="Wingdings" w:hAnsi="Wingdings"/></w:rPr></w:lvl></w:abstractNum><w:num w:numId="1"><w:abstractNumId w:val="0"/></w:num></w:numbering>'),
 'word/theme/theme1.xml': ('theme+xml', 'theme', '<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Office 主题"><a:themeElements><a:clrScheme name="Office"><a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1></a:clrScheme><a:fontScheme name="Office"><a:majorFont><a:latin typeface="Calibri Light"/><a:ea typeface=""/></a:majorFont></a:fontScheme></a:themeElements></a:theme>'),
}
with zipfile.ZipFile(sys.argv[1], 'w') as z:
    ov = ''.join(f'<Override PartName="/{n}" ContentType="{CT}{t}"/>' for n, (t, _, _) in parts.items())
    z.writestr('[Content_Types].xml', f'<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Override PartName="/word/document.xml" ContentType="{CT}wordprocessingml.document.main+xml"/>{ov}<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/><Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/></Types>')
    z.writestr('_rels/.rels', '<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="m" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/><Relationship Id="c" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/><Relationship Id="a" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/></Relationships>')
    z.writestr('docProps/core.xml', '<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:creator>王小明</dc:creator><cp:lastModifiedBy>王小明</cp:lastModifiedBy></cp:coreProperties>')
    z.writestr('docProps/app.xml', '<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"><Application>Microsoft Office Word</Application><Company>示例科技有限公司</Company></Properties>')
    rels = ''.join(f'<Relationship Id="r{i}" Type="{R}/{kind}" Target="{n[5:]}"/>' for i, (n, (_, kind, _)) in enumerate(parts.items()))
    z.writestr('word/_rels/document.xml.rels', f'<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">{rels}</Relationships>')
    z.writestr('word/document.xml', f'<w:document xmlns:w="{W}"><w:body><w:p><w:pPr><w:pStyle w:val="1"/></w:pPr><w:r><w:t>第一章</w:t></w:r></w:p><w:p><w:pPr><w:numPr><w:ilvl w:val="1"/><w:numId w:val="1"/></w:numPr></w:pPr><w:r><w:t>电话 13812345678</w:t></w:r></w:p></w:body></w:document>')
    for n, (_, _, body) in parts.items():
        z.writestr(n, body)
