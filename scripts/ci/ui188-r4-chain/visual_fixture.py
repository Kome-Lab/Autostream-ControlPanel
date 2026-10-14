"""Synthetic assets enter through real managed API; no internal result stubs."""
import base64,json,pathlib,subprocess,uuid

def prepare_visual(request,root,evidence):
    presets=[];candidates=[]
    for label,color in [('A','red'),('B','blue'),('C','green')]:
        existing=[p for p in request('GET','/video-cover-presets')['items'] if p['name']=='UI188 R4 acceptance '+label]
        if existing:
            assert len(existing)==1
            preset=request('GET','/video-cover-presets/'+existing[0]['id'])
            presets.append(preset)
            candidates.append({'name':'UI188 R4 '+label,'source':'preset','preset_id':preset['id'],'preset_revision':preset['revision'],'asset_id':preset['asset_id'],'variant_id':preset['asset_variant_id']})
            continue
        session=request('POST','/media-assets/upload-sessions',{},201)
        image=root/'data'/('acceptance-cover-'+__import__('os').environ['UI188_PROBE_ATTEMPT']+'-'+label+'.png')
        subprocess.run(['ffmpeg','-hide_banner','-loglevel','error','-f','lavfi','-i',f'color=c={color}:s=854x480','-frames:v','1','-threads','1',str(image)],check=True)
        boundary='ui188-r4-'+uuid.uuid4().hex
        fields=[('session_id',session['id']),('usage_type','video_cover')]
        multipart=b''.join((f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n').encode() for key,value in fields)
        multipart+=(f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="cover-{label}.png"\r\nContent-Type: image/png\r\n\r\n').encode()+image.read_bytes()+(f'\r\n--{boundary}--\r\n').encode()
        asset=request('POST','/media-assets',expected=201,raw=multipart,content_type='multipart/form-data; boundary='+boundary)
        variant=request('POST','/media-assets/'+asset['id']+'/variants',{'width':1920,'height':1080,'opaque':True})
        preset=request('POST','/video-cover-presets',{'name':'UI188 R4 acceptance '+label,'asset_id':asset['id'],'asset_variant_id':variant['id'],'enabled':True},201)
        presets.append(preset)
        candidates.append({'name':'UI188 R4 '+label,'source':'preset','preset_id':preset['id'],'preset_revision':preset['revision'],'asset_id':asset['id'],'variant_id':variant['id']})
    watermark=root/'data'/'acceptance-watermark-opaque.png'
    if not watermark.exists():
        subprocess.run(['ffmpeg','-hide_banner','-loglevel','error','-f','lavfi','-i','color=black@0:s=1920x1080,format=rgba,drawbox=x=20:y=20:w=180:h=100:color=white:t=fill:replace=1','-frames:v','1','-threads','1',str(watermark)],check=True)
    pixel=subprocess.run(['ffmpeg','-hide_banner','-loglevel','error','-i',str(watermark),'-vf','format=rgba,crop=1:1:70:56','-frames:v','1','-threads','1','-f','rawvideo','-pix_fmt','rgba','-'],capture_output=True,check=True).stdout
    assert pixel==bytes([255,255,255,255]), 'synthetic Watermark must contain an opaque white foreground'
    overlays=[p for p in (request('GET','/profiles/overlay') or []) if p['name']=='UI188 R4 acceptance watermark']
    assert len(overlays)<=1
    body={'name':'UI188 R4 acceptance watermark','config':{'watermark_enabled':True,'watermark_image_data_url':'data:image/png;base64,'+base64.b64encode(watermark.read_bytes()).decode(),'watermark_file_name':'r3-test.png'}}
    overlay=request('PUT','/profiles/overlay/'+overlays[0]['id'],body) if overlays else request('POST','/profiles/overlay',body,201)
    value={'overlay_profile_id':overlay['id'],'presets':presets,'candidates':candidates,'visual_settings':{'cover_candidates':candidates,'cover_source':'preset','cover_preset_id':presets[0]['id'],'cover_asset_id':presets[0]['asset_id'],'cover_variant_id':presets[0]['asset_variant_id'],'cover_start_active':True}}
    (evidence/('acceptance-visual-fixtures-'+__import__('os').environ['UI188_PROBE_ATTEMPT']+'.json')).write_text(json.dumps(value,indent=2)+'\n')
    return value
