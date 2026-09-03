import yaml
import os
import glob

class PluginRegistry:
    def __init__(self, plugins_dir):
        self.plugins_dir = plugins_dir
        self.plugins = {}
        self._load_plugins()

    def _load_plugins(self):
        # 递归扫描所有插件的 metadata (这里假设使用 plugin.yaml 作为元数据文件)
        pattern = os.path.join(self.plugins_dir, '**', 'plugin.yaml')
        for yaml_path in glob.glob(pattern, recursive=True):
            with open(yaml_path, 'r') as f:
                try:
                    metadata = yaml.safe_load(f)
                    if metadata and 'id' in metadata:
                        self.plugins[metadata['id']] = metadata
                except Exception as e:
                    print(f"Failed to load plugin metadata from {yaml_path}: {e}")

    def get_plugin_by_id(self, plugin_id):
        return self.plugins.get(plugin_id)

    def find_plugins_by_capability(self, capability, language=None):
        matches = []
        for plugin_id, meta in self.plugins.items():
            if meta.get('capability') == capability:
                if language:
                    # 如果指定了语言，且插件声明了语言，则必须匹配
                    plugin_lang = meta.get('language')
                    if plugin_lang and plugin_lang != language:
                        continue
                matches.append(meta)
        return matches

    def list_plugins(self):
        return list(self.plugins.values())
