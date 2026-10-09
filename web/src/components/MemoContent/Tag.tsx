import type { Element } from "hast";
import { useLocation } from "react-router-dom";
import { useAuth } from "@/contexts/AuthContext";
import { type MemoFilter, stringifyFilters, useMemoFilterContext } from "@/contexts/MemoFilterContext";
import useNavigateTo from "@/hooks/useNavigateTo";
import { colorToHex } from "@/lib/color";
import { tagStyles } from "@/lib/markdownStyles";
import { isMemoCollectionRoute } from "@/lib/memo-views";
import { findTagMetadata } from "@/lib/tag";
import { cn } from "@/lib/utils";
import { Routes } from "@/router";
import { withMemoFilter } from "../MemoView/navigation";

interface TagProps extends React.HTMLAttributes<HTMLSpanElement> {
  node?: Element; // AST node from react-markdown
  "data-tag"?: string;
  children?: React.ReactNode;
  parentPage?: string;
}

export const Tag: React.FC<TagProps> = ({ "data-tag": dataTag, children, className, style, node: _node, parentPage, ...props }) => {
  const location = useLocation();
  const navigateTo = useNavigateTo();
  const { getFiltersByFactor, removeFilter, addFilter } = useMemoFilterContext();
  const { userTagsSetting } = useAuth();

  const tag = dataTag || "";

  // Custom color from user tag metadata. Dynamic hex values must use inline styles
  // because Tailwind can't scan dynamically constructed class names.
  // Text uses a darkened variant (40% color + black) for contrast on light backgrounds.
  const metadata = userTagsSetting ? findTagMetadata(tag, userTagsSetting) : undefined;
  const bgHex = colorToHex(metadata?.backgroundColor);
  const tagStyle: React.CSSProperties | undefined = bgHex
    ? {
        borderColor: bgHex,
        color: `color-mix(in srgb, ${bgHex} 60%, black)`,
        backgroundColor: `color-mix(in srgb, ${bgHex} 15%, transparent)`,
        ...style,
      }
    : style;

  const handleTagClick = (e: React.MouseEvent) => {
    e.stopPropagation();

    // Detail and journal readers do not apply collection filters themselves.
    if (!isMemoCollectionRoute(location.pathname)) {
      const collection = parentPage && isMemoCollectionRoute(parentPage) ? parentPage : Routes.HOME;
      navigateTo(withMemoFilter(collection, stringifyFilters([{ factor: "tagSearch", value: tag }])));
      return;
    }

    const isActive = getFiltersByFactor("tagSearch").some((filter: MemoFilter) => filter.value === tag);
    if (isActive) {
      removeFilter((f: MemoFilter) => f.factor === "tagSearch" && f.value === tag);
    } else {
      // Remove all existing tag filters first, then add the new one
      removeFilter((f: MemoFilter) => f.factor === "tagSearch");
      addFilter({
        factor: "tagSearch",
        value: tag,
      });
    }
  };

  return (
    <span
      className={cn(tagStyles.base, "cursor-pointer transition-opacity hover:opacity-75", !bgHex && tagStyles.defaultColor, className)}
      style={tagStyle}
      data-tag={tag}
      {...props}
      onClick={handleTagClick}
    >
      {children}
    </span>
  );
};
