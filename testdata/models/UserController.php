<?php

namespace App\Http\Controllers;

use App\Models\Post;
use App\Models\User;

class UserController
{
    public function show(int $id): string
    {
        $user = User::find($id);
        return $user->email_address;
    }

    public function create(): string
    {
        $user = new User();
        return $user->email_address;
    }

    public function posts(int $id): array
    {
        $user = User::find($id);
        return $user->posts->toArray();
    }

    public function chainedProp(int $id): string
    {
        $user = User::find($id);
        return $user->posts->slug_url;
    }

    public function authorEmail(int $postId): string
    {
        $post = Post::find($postId);
        return $post->author->email_address;
    }

    public function price(int $id): string
    {
        $user = User::find($id);
        return $user->price;
    }
}
