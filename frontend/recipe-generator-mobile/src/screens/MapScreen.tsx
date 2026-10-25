import React, { useEffect, useMemo, useState } from 'react';
import { ActivityIndicator, Platform, ScrollView, TouchableOpacity, View } from 'react-native';
import { WebView } from 'react-native-webview';
import { NativeStackScreenProps } from '@react-navigation/native-stack';
import { RootStackParamList } from '../navigation/AppNavigator';
import apiService, { GraphEdge, RecipeLink } from '../services/apiService';
import { Recipe } from '../types';
import { useTheme } from '../context/ThemeContext';
import { useLanguage } from '../context/LanguageContext';
import { Chip, Text } from '../components/ui';
import { ALL_TAGS, tagLabelKey } from '../constants/tags';
import { space, radius } from '../theme';
import { useEscapeBack } from '../hooks/useEscapeBack';

// BACKLOG 18.3, third and final shape. Two ways to look at one library:
//
//   Overlap — recipes only, joined where they share food (scored by the backend's
//             /graph/overlap, i.e. the same rankSimilar as "recipes like this one", which
//             weighs a shared food by how rare it is — sharing salt is not sharing anything).
//             Cola runs once and settles; nodes stay draggable, the simulation does not
//             re-run. Ingredients are never drawn — they are what made the last two versions
//             a hairball — they appear as text when a recipe is tapped.
//   Explore — the opposite question. Pick a mood (spicy, quick, comfort food, a cuisine,
//             a technique) and watch the library narrow to the recipes that carry all of
//             them. Plain views, no canvas.
//
// ponytail: CDN scripts, no offline map; bundle cytoscape if that ever matters.

const FAMILY_COLORS = ['#3f5a43', '#a9634c', '#c9a13b', '#5b7fa6', '#8a5b8f', '#4f8f85', '#9c5b5b', '#6b7f3f'];
const NEUTRAL = '#9b9b9b';

type Family = { key: string; label: string; color: string };

const titleCase = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** The Overlap map: one self-contained page, an iframe on web and a WebView on native. */
const overlapPage = (
  nodes: unknown[],
  links: unknown[],
  families: Family[],
  bg: string,
  text: string,
  surface: string,
  hint: string,
  nothing: string
) => `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1">
<style>html,body,#c{margin:0;height:100%;background:${bg}}
#c{position:absolute;inset:0}
#legend,#info{position:absolute;font:12px -apple-system,system-ui,sans-serif;color:${text};background:${surface}ee;border-radius:10px;padding:8px 10px;max-width:60%}
#legend{top:8px;left:8px}
#info{bottom:8px;left:8px;right:8px;max-width:none;max-height:32%;overflow:auto}
.sw{display:inline-block;width:9px;height:9px;border-radius:9px;margin-right:6px}</style>
<script src="https://cdn.jsdelivr.net/npm/cytoscape@3.30.2/dist/cytoscape.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/webcola@3.4.0/WebCola/cola.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/cytoscape-cola@2.5.1/cytoscape-cola.js"></script></head>
<body><div id="c"></div><div id="legend"></div><div id="info">${hint}</div><script>
var families=${JSON.stringify(families)};
document.getElementById('legend').innerHTML=families.map(function(f){return '<div><span class="sw" style="background:'+f.color+'"></span>'+f.label+'</div>'}).join('');
var info=document.getElementById('info');
var cy=cytoscape({container:document.getElementById('c'),
 elements:{nodes:${JSON.stringify(nodes)},edges:${JSON.stringify(links)}},
 style:[
  {selector:'node',style:{'transition-property':'opacity, border-width','transition-duration':'180ms','label':'data(label)','font-size':12,'font-weight':'bold','color':'${text}','background-color':'data(color)','width':'data(size)','height':'data(size)','text-valign':'bottom','text-margin-y':3,'text-wrap':'ellipsis','text-max-width':110,'min-zoomed-font-size':10}},
  {selector:'edge',style:{'transition-property':'opacity','transition-duration':'180ms','width':'data(w)','line-color':'#b9b2a8','curve-style':'bezier','opacity':0.55}},
  {selector:'.dim',style:{'opacity':0.08}},
  {selector:'.pick',style:{'border-width':3,'border-color':'${text}'}}],
 layout:{name:'cola',infinite:false,maxSimulationTime:2500,animate:true,fit:true,randomize:false,nodeDimensionsIncludeLabels:true,nodeSpacing:function(){return 20},edgeLength:function(e){return e.data('len')},avoidOverlap:true}});
// Fit once it has settled. Belt and braces: cola emits layoutstop when it converges, but a
// WebView that throttles rAF can leave the graph mid-spread, so the timer refits regardless.
var fit=function(){cy.fit(undefined,40)};
cy.one('layoutstop',fit);setTimeout(fit,2800);
cy.on('tap','node',function(e){
 var n=e.target;
 cy.elements().addClass('dim');cy.elements().removeClass('pick');
 n.closedNeighborhood().removeClass('dim');n.addClass('pick');
 var rows=n.connectedEdges().map(function(ed){
   var o=ed.source().same(n)?ed.target():ed.source();
   var sh=(ed.data('shared')||[]).join(', ');
   return '<div>· <b>'+o.data('label')+'</b>'+(sh?' — '+sh:'')+'</div>'; // an edge can rest on cuisine alone
 }).join('');
 info.innerHTML='<b>'+n.data('label')+'</b>'+(rows||'<div>'+${JSON.stringify(nothing)}+'</div>');
});
cy.on('tap',function(e){if(e.target===cy){cy.elements().removeClass('dim').removeClass('pick');info.innerHTML=${JSON.stringify(hint)};}});
</script></body></html>`;

type Props = NativeStackScreenProps<RootStackParamList, 'Map'>;

export default function MapScreen({ navigation }: Props) {
  useEscapeBack();
  const { theme } = useTheme();
  const { t } = useLanguage();
  const [mode, setMode] = useState<'overlap' | 'explore'>('overlap');
  const [picked, setPicked] = useState<string[]>([]);
  const [data, setData] = useState<{ recipes: Recipe[]; edges: GraphEdge[]; links: RecipeLink[] } | null>(null);

  useEffect(() => {
    Promise.all([apiService.getRecipes(), apiService.getGraph(), apiService.getRecipeOverlap()])
      .then(([recipes, edges, links]) => setData({ recipes, edges, links }))
      .catch(() => setData({ recipes: [], edges: [], links: [] }));
  }, []);

  // A recipe's "family" is its cuisine; old recipes have none, so the first tag that is
  // not a meal slot stands in, and anything left over is grey.
  const { familyOf, families } = useMemo(() => {
    const cuisine = new Map<string, string>();
    (data?.edges ?? []).forEach(e => {
      if (e.src_type === 'recipe' && e.dst_type === 'cuisine') cuisine.set(e.src, e.dst);
    });
    const keyOf = (r: Recipe) => {
      const c = cuisine.get(String(r.id));
      if (c) return `cuisine:${c}`;
      const tag = ALL_TAGS.find(d => d.group !== 'meal_type' && (r.tags ?? []).includes(d.slug));
      return tag ? `tag:${tag.slug}` : '';
    };
    const keys = Array.from(new Set((data?.recipes ?? []).map(keyOf))).filter(Boolean).sort();
    const label = (k: string) =>
      k.startsWith('cuisine:') ? titleCase(k.slice(8)) : t(tagLabelKey(k.slice(4)));
    const list: Family[] = keys.map((k, i) => ({ key: k, label: label(k), color: FAMILY_COLORS[i % FAMILY_COLORS.length] }));
    const byKey = new Map(list.map(f => [f.key, f]));
    const other: Family = { key: '', label: t('map.otherFamily'), color: NEUTRAL };
    const lookup = new Map<number, Family>((data?.recipes ?? []).map(r => [r.id, byKey.get(keyOf(r)) ?? other]));
    const usesOther = Array.from(lookup.values()).some(f => f.key === '');
    return { familyOf: lookup, families: usesOther ? [...list, other] : list };
  }, [data, t]);

  const html = useMemo(() => {
    if (!data || !data.recipes.length) return null;
    const linked = new Set<number>();
    data.links.forEach(l => { linked.add(l.a); linked.add(l.b); });
    const top = Math.max(...data.links.map(l => l.score), 0.0001);
    const nodes = data.recipes.map(r => ({
      data: {
        id: String(r.id),
        label: r.recipename,
        color: (familyOf.get(r.id) ?? { color: NEUTRAL }).color,
        size: linked.has(r.id) ? 26 : 16,
      },
    }));
    const links = data.links.map(l => {
      const rel = l.score / top;
      return {
        data: {
          source: String(l.a),
          target: String(l.b),
          shared: l.shared,
          w: 1 + 4 * rel,
          len: 180 - 110 * rel, // more shared food, shorter rope
        },
      };
    });
    return overlapPage(nodes, links, families, theme.bg, theme.text, theme.surface, t('map.tapHint'), t('map.nothingShared'));
  }, [data, families, familyOf, theme, t]);

  // Explore: every attribute a recipe carries, from data that already exists — its tags,
  // its cuisine, its techniques. No new call, no new column.
  const attrsOf = useMemo(() => {
    const m = new Map<number, Set<string>>();
    (data?.recipes ?? []).forEach(r => m.set(r.id, new Set((r.tags ?? []).map(s => `tag:${s}`))));
    (data?.edges ?? []).forEach(e => {
      if (e.src_type !== 'recipe') return;
      const s = m.get(Number(e.src));
      if (!s) return;
      if (e.dst_type === 'cuisine') s.add(`cuisine:${e.dst}`);
      else if (e.dst_type === 'technique') s.add(`technique:${e.dst}`);
      else if (e.dst_type === 'tag' || e.dst_type === 'meal') s.add(`tag:${e.dst}`);
    });
    return m;
  }, [data]);

  const attrLabel = (key: string) => {
    const [kind, ...rest] = key.split(':');
    const value = rest.join(':');
    return kind === 'tag' && ALL_TAGS.some(d => d.slug === value) ? t(tagLabelKey(value)) : titleCase(value);
  };

  const matches = useMemo(
    () => (data?.recipes ?? []).filter(r => picked.every(a => attrsOf.get(r.id)?.has(a))),
    [data, attrsOf, picked]
  );

  // Only attributes that would still leave something on screen — a chip that leads to
  // nothing is a dead end, so it is not offered.
  const offered = useMemo(() => {
    const counts = new Map<string, number>();
    matches.forEach(r => attrsOf.get(r.id)?.forEach(a => counts.set(a, (counts.get(a) ?? 0) + 1)));
    picked.forEach(a => counts.delete(a));
    return Array.from(counts.entries())
      .filter(([, n]) => n > 0 && n < matches.length)
      .sort((a, b) => b[1] - a[1] || attrLabel(a[0]).localeCompare(attrLabel(b[0])))
      .map(([key, n]) => ({ key, n }));
  }, [matches, attrsOf, picked, t]);

  const toggle = (key: string) =>
    setPicked(prev => (prev.includes(key) ? prev.filter(k => k !== key) : [...prev, key]));

  const switcher = (
    <View style={{ flexDirection: 'row', gap: space.sm, padding: space.md }}>
      <Chip label={t('map.overlap')} selected={mode === 'overlap'} onPress={() => setMode('overlap')} />
      <Chip label={t('map.explore')} selected={mode === 'explore'} onPress={() => setMode('explore')} />
    </View>
  );

  if (!data) {
    return (
      <View style={{ flex: 1, backgroundColor: theme.bg, alignItems: 'center', justifyContent: 'center' }}>
        <ActivityIndicator color={theme.accent} />
      </View>
    );
  }

  if (!data.recipes.length) {
    return (
      <View style={{ flex: 1, backgroundColor: theme.bg, alignItems: 'center', justifyContent: 'center', padding: space.lg }}>
        <Text tone="muted">{t('map.empty')}</Text>
      </View>
    );
  }

  return (
    <View style={{ flex: 1, backgroundColor: theme.bg }}>
      {switcher}
      {mode === 'overlap' ? (
        html == null ? (
          <View style={{ flex: 1, alignItems: 'center', justifyContent: 'center' }}>
            <Text tone="muted">{t('map.empty')}</Text>
          </View>
        ) : Platform.OS === 'web' ? (
          React.createElement('iframe', { srcDoc: html, style: { flex: 1, border: 0, width: '100%', height: '100%' } })
        ) : (
          <WebView originWhitelist={['*']} source={{ html }} style={{ flex: 1 }} />
        )
      ) : (
        <ScrollView contentContainerStyle={{ padding: space.md, paddingTop: 0, gap: space.md }}>
          <Text variant="caption" tone="muted">{t('map.exploreHint')}</Text>

          <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: space.sm }}>
            {picked.map(key => (
              <Chip key={key} label={attrLabel(key)} selected onPress={() => toggle(key)} />
            ))}
            {offered.map(({ key, n }) => (
              <Chip key={key} label={`${attrLabel(key)} · ${n}`} onPress={() => toggle(key)} />
            ))}
            {picked.length > 0 && <Chip label={t('map.startOver')} onPress={() => setPicked([])} />}
          </View>

          <Text variant="label">{t('map.matches', { count: matches.length })}</Text>

          {matches.length === 0 ? (
            <Text tone="muted">{t('map.noMatches')}</Text>
          ) : (
            <View style={{ flexDirection: 'row', flexWrap: 'wrap', gap: space.sm, alignItems: 'center' }}>
              {matches.map(r => {
                const fam = familyOf.get(r.id) ?? { color: NEUTRAL, label: '', key: '' };
                return (
                  <TouchableOpacity
                    key={r.id}
                    onPress={() => navigation.navigate('CookingMode', { recipe: r })}
                    accessibilityRole="button"
                    style={{
                      backgroundColor: `${fam.color}33`,
                      borderColor: fam.color,
                      borderWidth: 1,
                      borderRadius: radius.full,
                      paddingVertical: space.sm + 2,
                      paddingHorizontal: space.lg,
                    }}
                  >
                    <Text variant="label" style={{ color: theme.text }}>{r.recipename}</Text>
                  </TouchableOpacity>
                );
              })}
            </View>
          )}
        </ScrollView>
      )}
    </View>
  );
}
