#!/usr/bin/env python3
"""
代码统计工具 - 统计项目中的代码行数
支持多种编程语言的代码行数、空行数、注释行数统计
"""

import os
import re
from collections import defaultdict
from pathlib import Path

# 文件扩展名到语言名称的映射
LANGUAGE_MAP = {
    # 主要编程语言
    '.py': 'Python',
    '.js': 'JavaScript',
    '.ts': 'TypeScript',
    '.java': 'Java',
    '.go': 'Go',
    '.rs': 'Rust',
    '.c': 'C',
    '.cpp': 'C++',
    '.cc': 'C++',
    '.cxx': 'C++',
    '.h': 'C/C++ Header',
    '.hpp': 'C++ Header',
    '.rb': 'Ruby',
    '.php': 'PHP',
    '.swift': 'Swift',
    '.kt': 'Kotlin',
    '.scala': 'Scala',
    '.cs': 'C#',
    '.fs': 'F#',
    '.fsx': 'F#',
    '.erl': 'Erlang',
    '.ex': 'Elixir',
    '.exs': 'Elixir',
    '.elm': 'Elm',
    '.clj': 'Clojure',
    '.cljs': 'ClojureScript',
    '.lua': 'Lua',
    '.r': 'R',
    '.m': 'Objective-C',
    '.mm': 'Objective-C++',
    '.dart': 'Dart',
    '.groovy': 'Groovy',
    '.pl': 'Perl',
    '.pm': 'Perl',
    
    # Shell 脚本
    '.sh': 'Shell',
    '.bash': 'Bash',
    '.zsh': 'Zsh',
    '.ps1': 'PowerShell',
    
    # Web 前端
    '.html': 'HTML',
    '.htm': 'HTML',
    '.css': 'CSS',
    '.scss': 'SCSS',
    '.sass': 'Sass',
    '.less': 'Less',
    '.jsx': 'JSX',
    '.tsx': 'TSX',
    '.vue': 'Vue',
    '.svelte': 'Svelte',
    
    # 数据/配置
    '.sql': 'SQL',
    '.json': 'JSON',
    '.xml': 'XML',
    '.yaml': 'YAML',
    '.yml': 'YAML',
    '.toml': 'TOML',
    '.ini': 'INI',
    '.md': 'Markdown',
    '.rst': 'reStructuredText',
}

# 注释规则: (单行注释起始符列表, 多行注释起始符, 多行注释结束符)
COMMENT_RULES = {
    'Python': (['#'], '"""', '"""'),
    'JavaScript': (['//'], '/*', '*/'),
    'TypeScript': (['//'], '/*', '*/'),
    'JSX': (['//'], '/*', '*/'),
    'TSX': (['//'], '/*', '*/'),
    'Java': (['//'], '/*', '*/'),
    'Go': (['//'], '/*', '*/'),
    'Rust': (['//'], '/*', '*/'),
    'C': (['//'], '/*', '*/'),
    'C++': (['//'], '/*', '*/'),
    'C/C++ Header': (['//'], '/*', '*/'),
    'Ruby': (['#'], '=begin', '=end'),
    'PHP': (['//', '#'], '/*', '*/'),
    'Swift': (['//'], '/*', '*/'),
    'Kotlin': (['//'], '/*', '*/'),
    'Scala': (['//'], '/*', '*/'),
    'C#': (['//'], '/*', '*/'),
    'F#': (['//'], '(*', '*)'),
    'Erlang': (['%'], '', ''),
    'Elixir': (['#'], '', ''),
    'Elm': (['--'], '{-', '-}'),
    'Clojure': ([';'], '', ''),
    'ClojureScript': ([';'], '', ''),
    'Lua': (['--'], '--[[', ']]'),
    'R': (['#'], '', ''),
    'Objective-C': (['//'], '/*', '*/'),
    'Objective-C++': (['//'], '/*', '*/'),
    'Dart': (['//'], '/*', '*/'),
    'Groovy': (['//'], '/*', '*/'),
    'Perl': (['#'], '=pod', '=cut'),
    'Shell': (['#'], '', ''),
    'Bash': (['#'], '', ''),
    'Zsh': (['#'], '', ''),
    'PowerShell': (['#'], '<#', '#>'),
    'SQL': (['--'], '/*', '*/'),
}

class CodeStats:
    def __init__(self):
        self.stats_by_lang = defaultdict(lambda: {
            'files': 0,
            'code_lines': 0,
            'blank_lines': 0,
            'comment_lines': 0,
            'total_lines': 0
        })
        self.all_files = []
        self.excluded_dirs = {'.git', '.svn', 'node_modules', 'vendor', '.venv', 'venv', 
                              '__pycache__', '.pytest_cache', 'dist', 'build', '.idea', 
                              '.vscode', 'target', 'bin', 'obj'}
    
    def is_code_file(self, filepath):
        """检查是否是代码文件"""
        ext = os.path.splitext(filepath)[1].lower()
        return ext in LANGUAGE_MAP
    
    def should_skip_dir(self, dirname):
        """检查是否应该跳过该目录"""
        return dirname in self.excluded_dirs or dirname.startswith('.')
    
    def get_comment_info(self, ext):
        """获取注释信息"""
        lang = LANGUAGE_MAP.get(ext, '')
        return COMMENT_RULES.get(lang, ([], None, None))
    
    def analyze_file(self, filepath):
        """分析单个文件"""
        ext = os.path.splitext(filepath)[1].lower()
        lang = LANGUAGE_MAP.get(ext, 'Unknown')
        
        try:
            with open(filepath, 'r', encoding='utf-8', errors='ignore') as f:
                lines = f.readlines()
        except Exception as e:
            print(f"Error reading {filepath}: {e}")
            return
        
        single_comment_markers, multi_start, multi_end = self.get_comment_info(ext)
        
        code_lines = 0
        blank_lines = 0
        comment_lines = 0
        in_multiline_comment = False
        
        for line in lines:
            stripped = line.strip()
            
            # 处理多行注释
            if multi_start and multi_end:
                if in_multiline_comment:
                    comment_lines += 1
                    if multi_end in stripped:
                        in_multiline_comment = False
                    continue
                elif multi_start in stripped:
                    comment_lines += 1
                    if multi_end not in stripped.split(multi_start)[1]:
                        in_multiline_comment = True
                    continue
            
            # 空行
            if not stripped:
                blank_lines += 1
            # 单行注释
            elif any(stripped.startswith(marker) for marker in single_comment_markers):
                comment_lines += 1
            else:
                # 检查行内注释
                has_comment = False
                for marker in single_comment_markers:
                    if marker in stripped:
                        # 简单的行内注释检测
                        parts = stripped.split(marker, 1)
                        if parts[0].strip():
                            code_lines += 1
                        else:
                            comment_lines += 1
                        has_comment = True
                        break
                if not has_comment:
                    code_lines += 1
        
        total = len(lines)
        
        self.stats_by_lang[lang]['files'] += 1
        self.stats_by_lang[lang]['code_lines'] += code_lines
        self.stats_by_lang[lang]['blank_lines'] += blank_lines
        self.stats_by_lang[lang]['comment_lines'] += comment_lines
        self.stats_by_lang[lang]['total_lines'] += total
        
        self.all_files.append({
            'path': filepath,
            'lang': lang,
            'code': code_lines,
            'blank': blank_lines,
            'comment': comment_lines,
            'total': total
        })
    
    def scan_directory(self, root_dir='.'):
        """扫描目录"""
        for dirpath, dirnames, filenames in os.walk(root_dir):
            # 过滤目录
            dirnames[:] = [d for d in dirnames if not self.should_skip_dir(d)]
            
            for filename in filenames:
                filepath = os.path.join(dirpath, filename)
                if self.is_code_file(filepath):
                    self.analyze_file(filepath)
    
    def generate_report(self):
        """生成统计报告"""
        total_files = sum(s['files'] for s in self.stats_by_lang.values())
        total_code = sum(s['code_lines'] for s in self.stats_by_lang.values())
        total_blank = sum(s['blank_lines'] for s in self.stats_by_lang.values())
        total_comment = sum(s['comment_lines'] for s in self.stats_by_lang.values())
        total_lines = sum(s['total_lines'] for s in self.stats_by_lang.values())
        
        # 按代码行数排序
        sorted_langs = sorted(
            self.stats_by_lang.items(),
            key=lambda x: x[1]['code_lines'],
            reverse=True
        )
        
        # 生成报告
        report = []
        report.append("=" * 80)
        report.append(" " * 25 + "代码统计报告")
        report.append("=" * 80)
        report.append("")
        
        # 总体统计
        report.append("📊 总体统计")
        report.append("-" * 80)
        report.append(f"  文件总数:     {total_files:>10,}")
        report.append(f"  代码行数:     {total_code:>10,} ({total_code/total_lines*100 if total_lines else 0:.1f}%)")
        report.append(f"  注释行数:     {total_comment:>10,} ({total_comment/total_lines*100 if total_lines else 0:.1f}%)")
        report.append(f"  空行数:       {total_blank:>10,} ({total_blank/total_lines*100 if total_lines else 0:.1f}%)")
        report.append(f"  总行数:       {total_lines:>10,}")
        report.append("")
        
        # 按语言分类统计
        report.append("📁 按语言分类统计")
        report.append("-" * 80)
        report.append(f"{'语言':<20} {'文件数':>8} {'代码行':>10} {'注释行':>10} {'空行':>8} {'总行数':>10}")
        report.append("-" * 80)
        
        for lang, stats in sorted_langs:
            report.append(
                f"{lang:<20} {stats['files']:>8,} {stats['code_lines']:>10,} "
                f"{stats['comment_lines']:>10,} {stats['blank_lines']:>8,} {stats['total_lines']:>10,}"
            )
        
        report.append("-" * 80)
        report.append(
            f"{'总计':<20} {total_files:>8,} {total_code:>10,} "
            f"{total_comment:>10,} {total_blank:>8,} {total_lines:>10,}"
        )
        report.append("")
        
        # 代码最多的文件 Top 10
        report.append("📄 代码量最多的文件 (Top 10)")
        report.append("-" * 80)
        top_files = sorted(self.all_files, key=lambda x: x['code'], reverse=True)[:10]
        for i, f in enumerate(top_files, 1):
            report.append(f"  {i:2}. {f['path']:<45} [{f['lang']:<12}] {f['code']:>6} 行")
        
        report.append("")
        report.append("=" * 80)
        
        return "\n".join(report)

def main():
    stats = CodeStats()
    print("🔍 正在扫描项目文件...")
    stats.scan_directory('.')
    print("✅ 扫描完成，正在生成报告...\n")
    
    report = stats.generate_report()
    print(report)
    
    # 保存报告到文件
    with open('code_stats_report.txt', 'w', encoding='utf-8') as f:
        f.write(report)
    print("\n💾 报告已保存到: code_stats_report.txt")

if __name__ == '__main__':
    main()
